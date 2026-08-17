# Claude 会话管理 - Wails 内嵌终端版（分支实验，路线 3）

真·终端内嵌 GUI 的 demo：**Go (Wails v2) + xterm.js + ConPTY**。
左侧会话列表（复用 `src/sessions.go` 的解析逻辑），右侧是完整交互式终端——
双击/点击会话 = 在 ConPTY 里跑 `claude -r <id>`，claude 的完整 TUI（方向键、/菜单等）都能用。

## 为什么是 Wails

- Fyne 没有终端组件，且 PopUp/事件机制不适合做终端（见 `src/` 的历史踩坑）
- Wails：Go 后端直接复用现有解析/运行逻辑；前端 WebView2（Win10/11 自带）渲染 xterm.js
- 单 exe、体积小（~10-20MB）；Windows 构建纯 Go，无需 CGO

## 目录

```
main.go            Wails 入口（嵌入 frontend/dist）
app.go             ConPTY 会话管理：StartSession/StartNew/TermWrite/TermResize
sessions.go        复制的会话解析（与 src/ 同源，后续可抽公共包）
frontend/          xterm.js 终端 + 会话列表（esbuild 打包）
wailsjs/           手写的 Go 绑定（与 wails generate 输出同格式）
```

## 本地构建

```powershell
cd wails-terminal/frontend
npm install
npm run build          # 产出 frontend/dist（含 index.html）
cd ..
go mod tidy
go build -tags webview2 -ldflags "-s -w -H windowsgui" -o claude-terminal.exe .
```

> `-H windowsgui`：GUI 子系统，双击不闪黑窗（与 wails build 一致）。
> 需要 WebView2 Runtime（Win10/11 一般已带 Edge 即有）。CI 里已有
> `wails-build.yml`（push 到本分支自动出 exe 产物）。

## 数据流

```
ConPTY 输出 → base64 → EventsEmit(term:data) → xterm.js
键盘输入 → term.onData → base64(UTF-8) → TermWrite → ConPTY 输入
窗口 resize → fit addon → TermResize(cols, rows) → ResizePseudoConsole
```

## 现状 / TODO

- [x] 会话列表 + 恢复（`claude -r`）/ 新建（组头 +）
- [x] 真终端：方向键、/命令、交互菜单、中文显示
- [ ] 运行状态徽标（agents --json）、自动刷新、搜索
- [ ] 收藏 / 别名 / 软删除（favorites.json 逻辑迁移）
- [ ] 图标（exe + 窗口）、打包（NSIS/portable）
- [ ] 把 sessions.go 抽成共享包，消除与 src/ 的重复
