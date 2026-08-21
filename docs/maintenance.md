# 维护与扩展手册

本项目是 Windows-only 的 Wails v2 应用：`main.go` 是 embed/Wails composition root，业务绑定和编排位于 `internal/app`，前端入口只负责创建 `app/bootstrap.js` 并启动应用。

## 架构与数据流

```
main.go
  └─ frontend/src/main.js
       └─ app/bootstrap.js
            ├─ terminal controller
            ├─ agent controller
            ├─ session controller
            ├─ settings controller
            └─ update controller
```

后端职责按领域划分：

- `internal/app`：Wails 公开方法、生命周期、业务编排和更新绑定。
- `internal/terminal`：ConPTY 启停、读写、resize、close、token 和命令外的进程边界；不依赖 Wails runtime，通过 callbacks 发事件。
- `internal/state`：兼容 `favorites.json`、`open-sessions.json`、`settings.json` 的 Store。所有读写共用互斥锁，更新在同一锁内完成，写入采用临时文件和原子替换。
- `internal/agent`：可启动/取消的 Watcher。后端通常约 1~2 秒拉取 `claude agents --json` 并推送 `agents:update`；前端每 30 秒调用 GetAgents 作为 watcher 缓存兜底，不是每 10 秒直接轮询。
- `internal/session`：扫描和解析 `~/.claude/projects/**/*.jsonl`。
- `internal/notify`：Windows 提示音。
- `internal/updater`：`v*-wails` Release 检查、下载和自替换。

前端目录：

- `app/bootstrap.js` 统一创建 controller、DOM、事件路由和生命周期。
- `terminal/` 处理 xterm、输入、粘贴、resize、主题和 terminal token。
- `agents/` 处理状态分类、完成边沿、未读徽标和提示动画。
- `sessions/` 处理列表、分组、折叠、归档、恢复、新会话 FIFO 配对；`pairing.js` 和 `view.js` 保持纯逻辑/DOM 边界。
- `settings/` 管理 UI theme、terminal theme、Shell 和设置菜单异步构建。
- `updates/` 管理检查/可用/应用状态机、进度和更新菜单。
- `state/` 是共享业务 state 的唯一来源；controller 不复制 active token、pending 或各类 Map/Set。
- `styles/` 按 tokens、base、sidebar、menus、terminal 分层；`style.css` 仅负责导入。
- `frontend/wailsjs/` 是提交到仓库的 Wails-compatible wrapper，`frontend/src/api/backend.js` 是前端唯一绑定边界。

事件路径：

```
ConPTY -> runtime term:data/term:exit -> terminal controller -> xterm/session UI
agent watcher -> agents:update -> agent controller -> badge/unread/toast
window resize -> terminal controller.resizeActive -> TermResize
update state/progress -> update controller -> settings menu
```

## 关键维护边界

### Terminal 并发与 token

`ptyRef.Close` 是幂等的；Write 和 Resize 在底层调用期间与 Close 互斥，唯一读协程允许 Close 并发中断阻塞 Read。Manager 用独立 lifecycle mutex 串行 Start/Kill/CloseAll，map/dimension/callback 由自身锁保护。

Start 采用“新 PTY 成功后再替换旧 PTY”；同 token 并发启动不会泄漏。旧 reader 退出时必须做 identity check，不能删除替换后的新实例或误发旧 Exit。`OpenIDs` 排序并过滤 `new-*`、nil 和 closed 实例。

前端新建会话先使用 `new-*` token。真实 JSONL ID 出现后，session controller 按目录 FIFO 建立 `realToNew/newToReal` 映射，并更新临时终端标签。关闭或归档映射会话时先把真实 ID 加入 `closedTokens`，再关闭临时 token；重新打开真实 ID 时由 openTab 清除该抑制，避免永久屏蔽 agents 完成事件。

### Agent watcher

Watcher 的 Start/Stop 可以安全重复；每次启动创建新的 child context、cancel 和 done channel。FetchFull 使用传入 context 和 10 秒超时，Stop 可以取消正在执行的命令。缓存有 ready 语义，成功的空列表也是已就绪；Get/Snapshot 和 emit 都返回副本，避免调用方修改缓存。

前端首个 agent 快照只建立基线。`working/queued -> done/消失` 和 interactive `busy -> idle` 产生一次完成提示；interactive 非忙消失产生会话结束提示。重新忙碌会清除结束闩锁。当前会话不标未读，后台会话标未读；手动关闭的真实 ID 由 `closedTokens` 抑制陈旧事件。

### State 事务

使用 `Store.SetAlias`、`SetHidden`、`SaveOpen`、`SetShell`，不要在 App 中写成 Load→修改→Save 两段事务。不存在的文件返回安全默认且无错误；损坏 JSON 或真实 I/O 错误同时返回安全默认和诊断错误。App 记录诊断后继续使用安全默认；用户触发的保存错误向 Wails 返回。

JSON 格式必须保持：

```json
// favorites.json
{"ids": [], "aliases": {}, "hidden": []}
// open-sessions.json
{"ids": []}
// settings.json
{"shell": "cmd"}
```

默认目录是 exe 同目录；测试通过 TempDir/注入目录隔离用户数据。临时文件替换失败也必须向上返回并清理残留。

### Update 状态机

前端更新 controller 的模式是 `idle -> checking -> ready -> applying -> idle`。检查失败、下载失败、应用失败都解除 busy/disabled 并允许重试；ready 信息跨菜单关闭/重开保留。进度经过 clamp，`重启中` 显示 toast。后端更新前持久化打开清单并关闭 ConPTY，更新成功后新进程恢复会话。

更新源只认 `v*-wails` 正式 Release；`.old`/`.new` 是 Windows 自替换的临时残留，启动时清理。更新只做非空和 MZ 头等粗校验，不做签名或哈希验证；exe 目录需要可写。

## 构建、测试与环境

前置条件：Windows 10/11、WebView2 Runtime、Go 1.25、Node 22/npm，以及需要运行真实集成测试时在 PATH 中的 Claude CLI。普通 Go/Node 测试不依赖真实 Claude 或 ConPTY。

```powershell
cd frontend
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
go build -tags "webview2 production" `
  -ldflags "-s -w -H windowsgui -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=dev" `
  -o claude-terminal.exe .
```

真实环境测试使用明确的 integration tag：

```powershell
go test -tags integration ./internal/terminal  # Windows ConPTY
go test -tags integration ./internal/agent     # claude agents --json
```

普通测试重点覆盖：state TempDir/事务、terminal fake PTY 与锁边界、agent fake Fetcher 取消、App 命令/存储编排、前端 Node 内置测试和 fake DOM。修改前端后必须先 `npm run build`，因为 `frontend/dist` 会被 Go embed。

## 扩展流程

1. 在 `internal/app` 增加导出 Wails 方法或对应领域编排；纯逻辑优先放入 `internal/*` 并补 Go 单元测试。
2. 更新或用 Wails CLI 重新生成 `frontend/wailsjs` wrapper；生成后审查方法集和路径，不要提交无关 model 文件。
3. 在 `frontend/src/api/backend.js` 保持唯一绑定边界，并同步 Node binding test。
4. 把 UI 行为放入对应领域 controller，通过 bootstrap 注入依赖和事件；不要再把业务逻辑堆进 `main.js`。
5. 补 Node fake/controller 测试，必要时补 Windows integration test；运行 `npm test`、`npm run build`、`go test ./...`、`go vet ./...`。

## 发布与限制

CI 对 PR/main 只做 validate；`v*-wails` tag 或手动触发才运行 Windows 交叉构建。tag 构建上传 `claude-terminal.exe`，只有 tag 发布 GitHub Release；手动触发只保留 artifact。

ConPTY 会结束更新前的会话进程，但 open-sessions 清单会供新版恢复。Claude 的 `/theme auto` 可能因 conhost 背景查询误判亮色，需要在 Claude 内手动 `/theme light`。未读状态是内存态。pwsh 必须是 PowerShell 7 的 `pwsh`，不可用时回退 cmd。
