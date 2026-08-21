# Claude 会话管理 - Wails 内嵌终端版

真·终端内嵌 GUI：**Go (Wails v2) + xterm.js + ConPTY**。
左侧会话列表，右侧多会话标签式真终端——每个会话的 claude 进程常驻后台，随时切换互不关闭。

> **维护/踩坑/扩展经验见 [`docs/maintenance.md`](docs/maintenance.md)**（含完整问题-修复时间线）。

## 功能

- 会话列表（按项目分组、组头折叠整组收起、**启动时默认全部折叠**；顶部全局眼睛开关：睁眼=折叠时露出运行中的会话，闭眼=折叠即全隐藏；展开时始终全量）
- 多会话终端：恢复/新建多个会话同时运行，随时切换、互不中断（无标签栏，切换/关闭全在左侧列表操作；当前终端对应会话行高亮，行尾 × 结束进程）
- 日间/夜间 UI 模式：左下角「⚙ 设置」切换（默认日间，记忆选择）；只影响外壳 UI
- 真终端：方向键、/命令、交互菜单、中文显示（每会话独立 xterm）
- 终端配色主题可切换：暗色（Claude 暖黑 / Dracula / One Dark / Solarized Dark / Nord）+ 亮色（Solarized Light / One Light / GitHub Light），🎨 按钮选择并记忆（与 UI 模式独立）
- 运行状态徽标：**绿色心跳=正在执行任务**、静态绿=已打开空闲、橙色⚠=等待权限、橙色●=后台待命、已完成自动标记"未读"
- 任务完成提示：心跳停止动画 + 顶部横幅 + 系统提示音 + 未读标记
- 项目折叠 / 会话重命名（本地别名）/ 会话归档（软隐藏 + 归档面板恢复）
- 收藏（favorites.json，与 fyne 版同格式可共用）
- 自动恢复上次打开的会话：记住关闭时所有仍打开的会话，下次启动全部恢复（open-sessions.json）
- 底层 Shell 可选：cmd.exe（默认）或 pwsh（PowerShell 7），左下角「⚙ 设置」变更；只影响之后新启动/恢复的会话（settings.json）
- 会话列表自动同步：每 5s 自动比对刷新——新建会话（claude 稍后才落盘 jsonl）会自动出现、claude 会话内 `/rename` 的名字也会自动跟随
- **一键更新到最新版**：左下角「⚙ 设置 → 更新 → 检查更新」，发现新版（GitHub Releases 里的 v*-wails 正式版）后可一键下载并**自动替换自身 + 重启进新版**（自动持久化当前打开的会话供新版恢复；下载进度实时显示）

## 目录

```
main.go             Wails 入口（embed frontend/dist）
internal/app/       Wails 绑定与业务编排
internal/terminal/  ConPTY 生命周期、输入输出与 token 管理
internal/session/   会话解析（~/.claude/projects/**/*.jsonl）
internal/agent/     claude agents --json 查询 + 调试日志
internal/state/     本地状态 favorites.json/open-sessions.json/settings.json
internal/notify/    Windows 提示音
internal/updater/   更新器：GitHub Releases 检查 / 下载 / 自替换 / 自动重启
frontend/           xterm.js + esbuild 打包
frontend/wailsjs/   Wails 生成绑定
assets/ + rsrc_windows_amd64.syso   图标
docs/maintenance.md 项目经验与维护手册（务必先读）
```

## 本地构建

```powershell
cd frontend
npm install
npm run build          # 产出 frontend/dist（含 index.html）
go mod tidy
# 注意：必须带 production tag！否则 Wails 会编译进"错误框占位实现"
# -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=... 注入版本号（更新器"当前版本"比对的依据）
go build -tags "webview2 production" -ldflags "-s -w -H windowsgui -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=v0.2-wails" -o claude-terminal.exe .
```

> `-H windowsgui`：GUI 子系统，双击不闪黑窗。需要 WebView2 Runtime（Win10/11 一般已带）。
> 调试用控制台版（可捕获日志）：去掉 `-H windowsgui` 另存一份。
> 若遇 `0x800700AA`（资源被占用），多为受限运行环境问题，真机一般正常。

## 发布与更新（一键更新到最新版）

更新机制（本次新增）：
- **源**：本仓库 GitHub Releases。CI（`.github/workflows/wails-build.yml`）在打 `v*-wails` tag 时构建
  `claude-terminal.exe` 并发布为 GitHub Release（`-pre`/`-rc` 结尾标为预发布）。
- **检查**：应用内「⚙ 设置 → 更新 → 检查更新」→ 后端查 GitHub API 列出 releases，只挑
  `v*-wails` 的**正式版**（跳过预发布与 fyne 版 tag），语义版本号最高者为"最新版"。
- **比较**：最新版 > 当前版本（`-X .../internal/app.Version=...` 注入的值；未注入的 "dev" 一律提示可更新）才算有更新。
- **更新**：点击后流式下载 exe（`update:progress` 事件实时百分比）→ 校验 PE 头 →
  持久化当前打开的会话 → 关闭所有 ConPTY → 自替换（当前 exe 改名为 `.old`，新版落地到原名）
  → 自动启动新版并退出本进程；新版启动时自动清理 `.old`/`.new` 残留。

发布新版流程：
```bash
git tag v0.2-wails && git push origin v0.2-wails   # 触发 CI 构建并发布 Release
```
> 版本号 tag 必须形如 `v<数字>[.数字][.数字][-wails]`（如 `v0.2-wails`）；预发布用 `v0.2-wails-rc`。
> 旧程序里的"检查更新"会提示这个新版，用户一键即可升级。

## 数据流

```
ConPTY 输出 -> base64 -> EventsEmit(term:data, token, b64) -> 对应 xterm
键盘输入 -> term.onData -> base64(UTF-8) -> TermWrite(token, b64) -> ConPTY
窗口 resize -> fit addon(让出1列) -> TermResize(token, cols, rows) -> ResizePseudoConsole
状态监听 -> 后端常驻监视器(1~2s) -> 变化即推 agents:update 事件 -> 徽标/未读/完成提示
```

## 已知限制

- claude 内 `/theme auto` 识别不到本软件终端的亮色背景：ConPTY 的
  conhost 拦截背景色查询并回复黑色。需在 claude 内手动 `/theme light`
  （或改 `~/.claude/settings.json` 的 theme 字段）；本软件不会干预
  claude 自己的主题设置。
- 终端快捷键：`Ctrl+V` / `Ctrl+Shift+V` / `Shift+Insert` 粘贴系统
  剪贴板；`Ctrl+Enter` 发送换行（LF）而不提交；其余组合键按 xterm
  默认（`Ctrl+C` 中断/选中即复制、`Ctrl+A` 行首等）。浏览器加速键
  （Ctrl+R/F/T 等）已被 wails 禁用，不会抢键。
- 未读标记为内存态：重启应用后不保留。
- 交互会话出现在 agents 列表的精确行为未在真机长时间验证。
- 底层 Shell 选 pwsh 需要系统装有 PowerShell 7（`pwsh` 在 PATH 中；
  Windows 自带的是 5.1，命令名 `powershell.exe` 而非 `pwsh`）。缺 pwsh 时
  选 pwsh 启动会话会失败，请装 [PowerShell 7](https://aka.ms/powershell-release)
  或切回 cmd。pwsh 模式下 claude 退出后会停留在 pwsh 提示符（`-NoExit`）。
