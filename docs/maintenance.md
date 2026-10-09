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
- `internal/state`：管理固定 LocalAppData 目录中的 `favorites.json`、`open-sessions.json`、`settings.json`、`projects.json` 和 `preferences.json`。所有读写共用进程内互斥锁；收藏、项目和界面设置的修改额外使用对应跨进程文件锁，在锁内重新读取并更新，写入采用临时文件和原子替换。
- `internal/agent`：可启动/取消的 Watcher。后端通常约 1~2 秒拉取 `claude agents --json` 并推送 `agents:update`；前端每 30 秒调用 GetAgents 作为 watcher 缓存兜底，不是每 10 秒直接轮询。
- `internal/session`：扫描和解析 `~/.claude/projects/**/*.jsonl`。
- `internal/usage`：解析 Claude usage、按 message ID 去重，并缓存会话/项目 token 汇总；由 App 的 `GetUsageSummary` 暴露只读查询。
- `internal/provider`：agent 注册中心与小接口适配层。`AgentProvider` 提供元数据和原始 ID 校验，`TerminalProvider` 准备启动方案；`HistoryProvider`、`StatusSource` 和 `UsageProvider` 按能力实现。Claude 包装原有 catalog/watcher/scanner，Codex 通过官方 app-server stdio `thread/list` 查询历史。生产注册 `CodexLive`，每个终端启动独立 stdio app-server，再启动原生 `codex --remote <loopback-url> --remote-auth-token-env <VAR>`；恢复时追加 `resume <UUID>`。
- `internal/notify`：Windows 提示音。
- `internal/updater`：`v*-wails` Release 检查、下载和自替换。

前端目录：

- `app/bootstrap.js` 统一创建 controller、DOM、事件路由和生命周期。
- `terminal/` 处理 xterm、输入、粘贴、resize、主题和 terminal token。
- `agents/` 处理状态分类、完成边沿、未读徽标和提示动画。
- `sessions/` 处理项目入口、会话列表目录分组、折叠、归档、恢复和新会话 FIFO 配对；`pairing.js` 和 `view.js` 保持纯逻辑/DOM 边界。
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

统一身份使用 `SessionRef{Provider, ID}`，`EncodeKey`/`DecodeKey` 集中处理内部键。Claude 保留旧裸 ID，Codex 内部键为 `codex:<UUID>`；列表的 `id` 是内部键，同时返回 `provider` 与 `rawId`。收藏、别名、隐藏、打开清单和前端 Map 均使用内部键，CLI 参数只使用已验证的原始 ID。其他 provider 的内部键可转义编码，但是否允许恢复由注册中心与该 provider 校验；adapter 输出的启动命令/参数必须适合公共 Shell 渲染边界，不能把内部键或未经校验的输入拼进命令。

顶部来源筛选使用共享 preference key `session-provider-filter`，值是排序去重且非空的 provider ID JSON 数组。`ListSessionsForProviders` / `ListHiddenSessionsForProviders` 在 registry 调用历史 adapter 前筛选来源，并返回 `sessions`、`succeededProviders` 和 `warnings`，空选择或未知 provider 返回错误。普通历史查询合并已选、已打开和待恢复来源；Codex 临时终端无须为此查询历史。归档只查询已选来源。`GetOpenSessionRefs` 由 Go 解码 canonical key、校验注册 provider，返回结构化身份，前端不解析内部键。Claude 基线按 provider 保留，仅成功查询更新；新建 Claude 前先读取历史基线，失败则不启动终端。catalog 的 `SnapshotWithWarnings` 将快照和有效性一起读取，缓存失败不会被误当成成功空列表。普通和归档请求各有递增序号，筛选改变和 stop 会使旧响应失效；晚到响应不能配对、渲染或覆盖提示。

新建 Codex 使用 `new-codex-*` runtime token，不进入 Claude 目录配对或公开 AdoptSession。观察器关联 `thread/start` / `thread/resume` 的请求与成功响应，取得权威主线程 ID；请求 ID 保留 string/integer 区别，resume 校验预期线程，递增序号拒绝乱序旧响应，子线程和 ephemeral 标题辅助线程不覆盖主线程。ID 分配与原生可恢复分开判断：`RestorableSource` 批量通过官方历史确认保存，成功 resume 也可直接确认。已确认 ID 不反复查历史；首次 turn 的开始/完成使负缓存失效，关闭前以 3 秒上限强制刷新未确认身份。历史查询显式包含 cli/vscode/appServer，并用空 modelProviders 数组包含自定义模型来源。

App 在启动完成、状态更新和关闭前调用 Manager 的原生身份绑定；只有原生保存确认与本地快照保存均成功才发送 `restorable=true`。Manager 保留 PTY token，恢复清单写 canonical `codex:<UUID>`。`/new` 切到未保存线程时，以空绑定显式禁止旧 token 回退进入恢复清单；新的历史确认后再绑定。前端同步正式行和标题，历史尚未返回时使用正式 fallback 行，失败/未保存时保留等待行；状态使用 canonical 行身份，Token 仍按 runtime token 查询。恢复 token 被原生切换后，旧历史的显示、打开、关闭和重命名都检查终端归属，避免作用到新会话。

Claude watcher 与 Codex observer 只向容量为 1 的合并队列发信号，更新循环取得 App launch 锁后重新检查生命周期再推送，避免 watcher.Stop 的锁等待环。Shutdown 在 launch 锁下通过 `RestorationSealer` 固定最后的权威身份快照，然后确认保存、持久化并关闭 PTY；此边界后的协议事件不再改变恢复身份。原生重命名仍使用 `RenameProvider`：Claude 调用内嵌 SDK 的离线 `renameSession`，Codex 调用 `thread/name/set`，成功后刷新缓存。原生名称不被本地 alias 覆盖，删除/归档仍保留 hidden 语义。依赖、打包和独立集成测试见 [原生重命名](native-rename.md)。

透明转发保持原始消息、顺序和未知字段，只添加/去除 stdio 的换行 framing；观察器接收副本，不回答审批或用户输入请求。转发器只绑定 `127.0.0.1:0`、使用 256-bit 随机 bearer、拒绝浏览器 Origin 和第二客户端。凭据仅加入该 PTY 环境，大小写不敏感地替换同名环境变量，不写入父进程环境、日志或状态文件。`LaunchSpec.Env` 与 `Close` 将 server/relay 作为终端专属资源交给 Manager，启动失败时 App 清理，成功后 reader 退出、替换、Kill 和 CloseAll 均由幂等 `ptyRef.Close` 清理。专属 server 的运行期由 PTY 所有权控制，避免 Wails context 提前取消导致恢复清单被退出事件擦除；Shutdown 先持久化并关闭终端，再取消其他读请求，启动与关闭由 App launch 锁串行。

Codex `thread/status/changed` 映射 active/idle/systemError/notLoaded 与审批/输入标记，`turn/completed` 保留 completed/failed/interrupted。断线显示 unknown，只有显式 completed 才产生成功提示；Claude 原有状态推断不变。Claude watcher 与 Codex 事件合并后推送同一 `agents:update`，Codex 通过 runtimeToken 更新临时行，外部终端不猜测。

Codex 用量只采用 `thread/tokenUsage/updated` 的累计 total 与最近 last 快照，重复事件替换而不累加。input 已含 cache read/write，适配时扣除缓存部分得到普通 input；reasoning 是 output 的明细，不额外加到总量。校验非负、分类范围、必需字段和 total=input+output；异常保留旧快照并标旧。模型请求数、缓存层级和项目总量不猜测；Codex 不进入 Claude project prefetch/cache。新开终端时，快照缓存以 1024 项为收敛目标，优先清理最旧的非活动项；活动终端保留，应用重启后不宣称旧快照可用。公开 thread/read/resume/Turn schema 没有历史 usage 字段，0.161.0 的合成历史恢复实验未回放旧 token 事件。

Codex 历史读端有 10 秒缓存，失败时保留最后成功快照并记录诊断；单次完整分页有 15 秒 deadline、重复 cursor 防护和有界 JSON 消息。只启动/关闭本应用自己的原生 app-server，不调用全局 daemon stop，不解析 Codex 私有 SQLite/JSONL。App 生命周期 context 在重启初始化/Shutdown 时取消，停止在途读请求。缺失 CLI、异常响应和启动失败不创建新终端记录。

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
// projects.json
{"dirs": ["C:\\work\\project"], "favorites": ["C:\\work\\project"]}
// preferences.json
{"ui-theme": "dark", "term-theme": "nord", "term-font-size": "18", "terminal-layout-mode": "grid-2x2"}
```

项目配置中的每个元素就是用户选择并保存的工作目录字符串，不是独立的运行时对象；`favorites` 可省略，空白目录项会被忽略。默认文件目录固定为 `%LOCALAPPDATA%\ClaudeSessionManager`，不随 exe 文件名、路径或版本变化；测试通过 TempDir/注入目录隔离用户数据。旧 exe 目录 JSON、旧 `config.json/runtime.json` 和旧 WebView 设置均不自动导入，也不删除或覆盖。

界面主题、终端配色/字号、布局/分屏比例以及每日更新检查日期通过 `LoadPreferences` / `SetPreference` 读写 `preferences.json`。前端共享内存缓存按键串行保存；启动先加载设置，再初始化布局、主题和会话恢复。读取失败时不会把默认值回写损坏的配置；保存失败显示状态提示，重复设置同一值可重试。Wails 显式使用固定 `WebView2` 子目录，调试日志为同一数据目录下的 `agents-debug.log`。同时运行多个实例时，配置按键更新；打开会话清单仍为最近一次保存的快照。

项目存储和 App 边界必须保持以下语义：缺失 `projects.json` 返回安全空列表，损坏文件返回安全空列表并保留错误；添加、删除和收藏修改在跨进程锁内完成，使用规范化目录 identity，重复目录幂等成功；删除不存在的目录也幂等成功。App 的 `ListProjects`、`ChooseProjectDir`、`AddProject`、`OpenFolder`、`ListProjectFavorites`、`SetProjectFavorite`、`DeleteProject` 分别负责读取、打开原生目录选择器、校验并保存目录、校验目录并启动 Windows 文件资源管理器、读取/更新收藏、移除配置。收藏使用目录规范化值持久化，新收藏置顶，取消收藏幂等；选择取消不保存；打开文件夹只接受存在的目录；删除只移除配置、不删除真实目录、历史会话或终端，并清理对应收藏。

前端启动时先加载项目目录和收藏，再按规范化完整路径把它们合并到会话列表；因此没有会话的目录仍以空分组可见，已保存但后来不存在的目录也保持可见。顶部只显示「项目」和加号，选择成功后空目录分组立即出现，收藏目录分组排在普通目录之前且保持收藏顺序，文件夹图标使用高亮色。项目目录和会话自动生成的目录右键都提供添加/取消收藏和打开文件夹；目录分组的 `+` 直接复用 `StartNew(dir)`，打开文件夹失败时显示状态提示。应用重启后目录和收藏从 `projects.json` 恢复，而会话恢复使用会话记录自身的 `dir`，不从项目目录列表推导。前端不提供删除按钮；`DeleteProject` 仅作为后端/绑定兼容能力保留。

### Update 状态机

前端更新 controller 的模式是 `idle -> checking -> ready -> applying -> idle`。检查失败、下载失败、应用失败都解除 busy/disabled 并允许重试；ready 信息跨菜单关闭/重开保留。进度经过 clamp，`重启中` 显示 toast。后端更新前持久化打开清单并关闭 ConPTY，更新成功后新进程恢复会话。

更新源只认 `v*-wails` 正式 Release；`.old`、`.update-*.old` 和 `.new` 是 Windows 自替换的临时残留，启动时尝试清理。旧程序备份带随机后缀，避免上次备份因旧进程尚未退出而阻止下一次更新。更新核对 Release 资产 SHA256、实际下载大小和 MZ 头，但不做代码签名验证；exe 目录需要可写。更新前保存打开会话失败时不会关闭终端或替换程序。

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
New-Item -ItemType Directory -Force .codex-build | Out-Null
go build -tags "webview2 production" `
  -ldflags "-s -w -H windowsgui -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=dev" `
  -o .codex-build/claude-terminal-dev.exe .
```

真实环境测试使用明确的 integration tag：

```powershell
go test -tags integration ./internal/terminal  # Windows ConPTY
go test -tags integration ./internal/agent     # claude agents --json
```

Codex 的真实 Windows 检查独立执行，普通单测仅使用 fake 传输和临时目录：

```powershell
go test -tags integration ./internal/provider -run 'TestCodex' -v
```

这些测试在隔离 CODEX_HOME 中检查历史/恢复 RPC、ConPTY 启动、原生 remote TUI 新建/resume、身份与清理。实时用量测试使用 loopback Responses SSE 假模型，让真实 Codex 执行唯一合成提示，验证 completed、TUI 回复和 input/cache/output/reasoning 快照贯通；主请求恰好一次，已识别的独立标题 schema 请求由假服务返回合法 JSON，不能混入主线程统计。不使用真实凭据或会话，不调用外部模型。合成历史/模型夹具限定 `codex-cli 0.161.0`，其他版本明确 skip；普通 ConPTY 启动测试独立运行。这些检查不能代替真实模型任务、真实审批交互或 GUI 人工验收。

普通测试重点覆盖：state TempDir/事务、terminal fake PTY 与锁边界、agent fake Fetcher 取消、App 命令/存储编排、前端 Node 内置测试和 fake DOM。修改前端后必须先 `npm run build`，因为 `frontend/dist` 会被 Go embed。

项目目录相关的最小验证命令：

```powershell
cd frontend
node --test test/app-bootstrap.test.js test/backend-bindings.test.js test/projects-controller.test.js test/session-controller.test.js
node --check src/app/bootstrap.js
node --check src/sessions/controller.js
node --check src/sessions/view.js
cd ..
go test ./internal/state ./internal/app
git diff --check
```

这些测试覆盖项目和收藏保存/重启读取、收藏置顶、缺失或损坏配置、规范化重复目录、单行项目入口、空目录分组、选择取消与添加、目录分组加号、项目及会话自动生成目录的右键收藏、失效目录新建失败，以及 Wails wrapper 方法集合和参数转发。Go/App 测试另外覆盖兼容性 `DeleteProject` 只移除配置、不触碰真实目录和已有会话的边界；前端不提供删除确认或删除按钮。

## 扩展流程

1. 在 `internal/app` 增加导出 Wails 方法或对应领域编排；纯逻辑优先放入 `internal/*` 并补 Go 单元测试。
2. 更新或用 Wails CLI 重新生成 `frontend/wailsjs` wrapper；生成后审查方法集和路径，不要提交无关 model 文件。
3. 在 `frontend/src/api/backend.js` 保持唯一绑定边界，并同步 Node binding test。
4. 把 UI 行为放入对应领域 controller，通过 bootstrap 注入依赖和事件；不要再把业务逻辑堆进 `main.js`。
5. 补 Node fake/controller 测试，必要时补 Windows integration test；运行 `npm test`、`npm run build`、`go test ./...`、`go vet ./...`。

## 发布与限制

CI 对 PR/main 只做 validate；`v*-wails` tag 或手动触发才运行 Windows 交叉构建。tag 构建上传 `claude-terminal.exe`，只有 tag 发布 GitHub Release；手动触发只保留 artifact。发布前把中文说明写入 `docs/releases/<tag>.md` 并随代码提交；CI 从该文件填充 Release 正文，文件缺失或为空时发布失败。正式版说明可用标题、无序列表、粗体、行内代码和 HTTPS 链接；应用会以安全的 DOM 节点展示这些格式，其他 Markdown 会作为普通文字显示。

ConPTY 会结束更新前的会话进程，但 open-sessions 清单会供新版恢复。Claude 的 `/theme auto` 可能因 conhost 背景查询误判亮色，需要在 Claude 内手动 `/theme light`。未读状态是内存态。pwsh 必须是 PowerShell 7 的 `pwsh`，不可用时回退 cmd。
