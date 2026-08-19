# Claude 会话管理 - Wails 内嵌终端版

真·终端内嵌 GUI：**Go (Wails v2) + xterm.js + ConPTY**。
左侧会话列表，右侧多会话标签式真终端——每个会话的 claude 进程常驻后台，随时切换互不关闭。

> **维护/踩坑/扩展经验见 [`EXPERIENCE.md`](EXPERIENCE.md)**（含完整问题-修复时间线）。

## 功能

- 会话列表（按项目分组、组头折叠整组收起；顶部全局眼睛开关：睁眼=折叠时露出运行中的会话，闭眼=折叠即全隐藏；展开时始终全量）
- 多会话终端：恢复/新建多个会话同时运行，随时切换、互不中断（无标签栏，切换/关闭全在左侧列表操作；当前终端对应会话行高亮，行尾 × 结束进程）
- 日间/夜间 UI 模式：左下角「⚙ 设置」切换（默认日间，记忆选择）；只影响外壳 UI
- 真终端：方向键、/命令、交互菜单、中文显示（每会话独立 xterm）
- 终端配色主题可切换：暗色（Claude 暖黑 / Dracula / One Dark / Solarized Dark / Nord）+ 亮色（Solarized Light / One Light / GitHub Light），🎨 按钮选择并记忆（与 UI 模式独立）
- 运行状态徽标：**绿色心跳=正在执行任务**、静态绿=已打开空闲、橙色⚠=等待权限、橙色●=后台待命、已完成自动标记"未读"
- 任务完成提示：心跳停止动画 + 顶部横幅 + 系统提示音 + 未读标记
- 项目折叠 / 会话重命名（本地别名）/ 会话归档（软隐藏 + 归档面板恢复）
- 收藏（favorites.json，与 fyne 版同格式可共用）
- 自动恢复上次会话：记住最后打开/使用过的会话，下次启动自动打开（last-session.json）

## 目录

```
main.go             Wails 入口（embed frontend/dist）
app.go              App 绑定 + 多会话 ConPTY 管理（token 路由 + 幂等关闭）
app_agents.go       App 方法：常驻状态监视器 / GetAgents / DebugLog
app_favorites.go    App 方法：重命名 / 归档 / 恢复
internal/session/   会话解析（~/.claude/projects/**/*.jsonl）
internal/agent/     claude agents --json 查询 + 调试日志
internal/favorites/ 本地状态 favorites.json（别名/归档/收藏）
frontend/           xterm.js + esbuild 打包
wailsjs/            Go 绑定（与 wails generate 同格式）
assets/ + rsrc_windows_amd64.syso   图标
EXPERIENCE.md       项目经验与维护手册（务必先读）
run.log             问题-修复时间线
```

## 本地构建

```powershell
cd wails-terminal/frontend
npm install
npm run build          # 产出 frontend/dist（含 index.html）
cd ..
go mod tidy
# 注意：必须带 production tag！否则 Wails 会编译进"错误框占位实现"
go build -tags "webview2 production" -ldflags "-s -w -H windowsgui" -o claude-terminal.exe .
```

> `-H windowsgui`：GUI 子系统，双击不闪黑窗。需要 WebView2 Runtime（Win10/11 一般已带）。
> 调试用控制台版（可捕获日志）：去掉 `-H windowsgui` 另存一份。
> 若遇 `0x800700AA`（资源被占用），多为受限运行环境问题，真机一般正常。

## 数据流

```
ConPTY 输出 -> base64 -> EventsEmit(term:data, token, b64) -> 对应 xterm
键盘输入 -> term.onData -> base64(UTF-8) -> TermWrite(token, b64) -> ConPTY
窗口 resize -> fit addon(让出1列) -> TermResize(token, cols, rows) -> ResizePseudoConsole
状态监听 -> 后端常驻监视器(1~2s) -> 变化即推 agents:update 事件 -> 徽标/未读/完成提示

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
```