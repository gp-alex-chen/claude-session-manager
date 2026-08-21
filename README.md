# Claude 会话管理器

Windows 原生 Claude 会话管理器：Go/Wails v2、xterm.js 与 ConPTY 组成右侧多会话终端，左侧列表负责分组、折叠、恢复和归档；切换和关闭都在左侧会话列表完成。

## 功能

- 多会话 ConPTY 终端：会话后台保持运行，切换互不关闭；支持恢复、新建、重命名、归档和关闭。
- 会话按项目分组；折叠、全局眼睛筛选、完成徽标、未读提示和完成动画保持独立。
- 左下角「⚙ 设置」管理日间/夜间 UI 主题、8 套终端主题和底层 Shell（cmd / pwsh）。
- 设置中的「更新」只检查 GitHub Releases 的 `v*-wails` 正式版本，可显示下载进度并自动替换重启。
- 本地状态兼容 `favorites.json`、`open-sessions.json`、`settings.json`，默认位于 exe 同目录。

## 前置条件

- Windows 10/11 与 WebView2 Runtime。
- Go 1.25、Node.js 22/npm。
- Claude CLI 在 `PATH` 中；真实 agents/ConPTY 集成测试也需要它。
- 普通构建不要求安装 Wails CLI：仓库提交了 `frontend/wailsjs` 的兼容 wrapper，后端可直接用 `go build`。

## 目录

```
main.go                         Wails composition root：embed、启动、绑定
internal/app/                   Wails App 绑定与业务编排
internal/terminal/              ConPTY 生命周期、输入输出、token 管理
internal/state/                 favorites/open-sessions/settings 原子状态存储
internal/session/               ~/.claude/projects 会话解析
internal/agent/                 claude agents watcher 与调试日志
internal/notify/                Windows 提示音
internal/updater/               GitHub 更新检查、下载、自替换
frontend/src/app/                应用 bootstrap 与统一生命周期
frontend/src/terminal/           终端 controller
frontend/src/agents/             agent 状态 controller
frontend/src/sessions/           会话 controller、配对 helper、列表 view
frontend/src/settings/           设置 controller
frontend/src/updates/            更新 controller
frontend/src/state/              前端共享 state
frontend/src/styles/             themes/base/sidebar/menus/terminal 分层 CSS
frontend/src/themes/             终端主题 catalog
frontend/src/api/backend.js     前端唯一 Wails wrapper 边界
frontend/wailsjs/                提交到仓库的 Wails 兼容绑定 wrapper
frontend/test/                   Node 内置测试（纯逻辑与 fake DOM）
assets/                          源资源；syso 留在 main 包目录
docs/maintenance.md              维护、测试与扩展手册
```

## 可复现开发命令

```powershell
cd frontend
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
go build -tags "webview2 production" `
  -ldflags "-s -w -H windowsgui -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=v0.2-wails" `
  -o claude-terminal.exe .
```

`-H windowsgui` 生成 GUI 子系统版本；调试时可去掉它。普通 Go 单元测试使用 fake、临时目录和依赖注入，不需要本机 Claude 会话。真实环境测试明确使用 integration tag：

```powershell
go test -tags integration ./internal/terminal
go test -tags integration ./internal/agent
```

这两组 Windows 集成测试分别依赖真实 ConPTY 和 `claude agents --json`。

## 数据流与状态文件

```
ConPTY 输出 -> base64 -> term:data(token, b64) -> 对应 xterm
键盘输入 -> UTF-8/base64 -> TermWrite -> ConPTY
窗口 resize -> FitAddon -> TermResize -> ConPTY
agent watcher（约 1~2s） -> agents:update -> 徽标/未读/完成提示
前端每 30s GetAgents 兜底 -> 使用后端 watcher 缓存
```

- `favorites.json` 保存会话 ID、显示别名和隐藏 ID。
- `open-sessions.json` 保存关闭应用时仍运行的会话 ID。
- `settings.json` 保存 `cmd`/`pwsh` 选择。
- 写入使用同一 Store 锁和临时文件替换；读取损坏时返回安全默认并记录诊断。

## 发布与更新

CI 对 `v*-wails` tag 构建 `claude-terminal.exe` 并发布 GitHub Release；`-pre`/`-rc` tag 标为预发布。手动触发可构建 artifact，但不会创建 release。应用更新只选择 `v*-wails` 正式版本，版本通过 `internal/app.Version` 注入。

```bash
git tag v0.2-wails
git push origin v0.2-wails
```

更新是粗粒度 PE 头校验，不提供签名/哈希验证；应用目录必须可写。更新前会结束 ConPTY，会话清单先持久化，重启后恢复。

## 已知限制

- ConPTY/conhost 可能让 Claude 的 `/theme auto` 误判亮色背景；需要在 Claude 内手动 `/theme light`。
- `Ctrl+V`、`Ctrl+Shift+V`、`Shift+Insert` 粘贴；`Ctrl+Enter` 发送 LF。未读状态只保存在内存中。
- pwsh 需要 PowerShell 7 的 `pwsh` 在 PATH；不可用时后端会回退 cmd 并记录诊断。

更多架构约定、并发边界、测试分层和扩展步骤见 [`docs/maintenance.md`](docs/maintenance.md)。
