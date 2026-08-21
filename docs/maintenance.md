# Claude 会话管理（Wails）· 项目经验与维护手册

> 记录本项目的架构、构建、测试、踩坑与扩展方法，供任何环境后续接手。
> 时间线内的问题均有原因 + 修复，可快速定位同类问题。

---

## 1. 项目一句话

**Go (Wails v2) + xterm.js + ConPTY** 的 Claude 会话管理终端：左侧会话列表（按项目分组、折叠、重命名、软删除、运行状态徽标），右侧多会话标签式真终端（每个会话独立 ConPTY 常驻，随时切换互不关闭）。

## 2. 技术栈与目录

```
main.go            Wails 入口（embed frontend/dist，窗口配置）
app.go             App 绑定 + 多会话 ConPTY 管理：terms map[token]*ptyRef
app_agents.go      App 方法：常驻状态监视器 / GetAgents / DebugLog
app_favorites.go   App 方法：重命名 / 归档 / 恢复
app_update.go      App 方法：GetVersion / CheckForUpdate / UpdateToLatest（一键更新）
internal/session/  会话解析：~/.claude/projects/**/*.jsonl
internal/agent/    claude agents --json 查询（CREATE_NO_WINDOW）+ 调试日志
internal/state/     本地状态：favorites.json/open-sessions.json/settings.json
internal/updater/  更新器：GitHub Releases 检查 / 下载 / 自替换 / 自动重启
frontend/          xterm.js + esbuild 打包（src -> dist）
wailsjs/           手写 Go 绑定（与 wails generate 输出同格式）
assets/ + rsrc_windows_amd64.syso   图标（Go 构建自动链接）
favorites_test.go / repro_diagnostic_test.go / internal/agent/agents_test.go
run.log            完整问题-修复时间线（本手册的精炼版）
```

> 目录整理约定（2026-08-18）：Wails 工程位于仓库根目录，绑定方法必须留在根目录 main 包
> （wailsjs 绑定 main.App；且 `//go:embed all:frontend/dist` 不支持 `../`，
> 入口必须留在根），纯逻辑按领域拆入 `internal/` 子包。

数据流：

```
ConPTY 输出 -> base64 -> EventsEmit(term:data, token, b64) -> 对应 xterm 实例
键盘输入 -> term.onData -> base64 -> TermWrite(token, b64) -> 对应 ConPTY
窗口缩放 -> fit addon(让出1列) -> TermResize(token, cols, rows)
状态轮询 -> GetAgents() 每10s -> 徽标/置顶区/未读逻辑
```

## 3. 构建与测试（Windows）

```powershell
# 前端
cd frontend
npm install
npm run build          # esbuild -> dist/（index.html 一并复制）

# 后端 —— 必须带 webview2 production 两个 tag！
cd ..
go build -tags "webview2 production" -ldflags "-s -w -H windowsgui -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=v0.2-wails" -o claude-terminal.exe .
# 调试用控制台版（可捕获 stderr 日志）：
go build -tags "webview2 production" -ldflags "-s -w -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=dev" -o claude-terminal-console.exe .

# 测试
go test ./...          # 需要 claude 在 PATH（TestFetchAgents 会真调 agents --json）
```

开发环境备注：
- go.mod 要求 go 1.25.0；本机 go1.23.4 + GOTOOLCHAIN=auto 会自动用本地缓存的 toolchain（`$USERPROFILE\go\pkg\mod\golang.org\toolchain*`）。
- 建议缓存指向项目内：`GOPATH=D:\plug\.gopath GOCACHE=D:\plug\.gocache GOTMPDIR=D:\plug\.gotmp`。
- Windows 构建纯 Go 无 CGO；需要 WebView2 Runtime（Win10/11 自带）。

## 4. 运行依赖

- `claude` CLI 在 PATH（npm 全局或 WinGet），版本实测 2.1.234。
- `~/.claude/projects/**/*.jsonl` 会话数据源。
- `favorites.json` 在 **exe 同目录**（与 fyne-sidebar 版同格式，可共用）。

## 5. 关键设计决策

### 5.1 多会话：token 体系
- 每个会话一个 `ptyRef`（ConPTY 包装），存于 `App.terms map[token]*ptyRef`。
- token：恢复的会话 = 会话 ID（UUID）；新建会话 = `new-<时间戳>`。
- 事件携带 token 路由到正确前端终端：`term:data(token, b64)` / `term:exit(token)`。
- 切换会话不关闭旧 ConPTY，进程后台常驻。

### 5.2 ptyRef：Close 幂等（防堆损坏闪退）
- 背景：恢复第二个会话时 killLocked 强关第一个 ConPTY，其读协程退出时还会再 Close 一次 → 句柄值被新会话复用 → 第二次 CloseHandle 关掉新会话活句柄 → **0xc0000374 堆损坏闪退**。
- 修复：`ptyRef.close()` 内置 closed 标记 + 锁，**Close 只真正执行一次**；写/缩放走后端检查，不再碰已关闭句柄。

### 5.3 状态监控语义（2026-08-18 实测）
`claude agents --json` 真实 schema：

```json
{ "id","cwd","kind","startedAt","sessionId","name",
  "state":"working|done|blocked", "status":"busy|idle|waiting",
  "waitingFor","pid" }
```

| 前端状态 | 判定 | 显示 |
|---|---|---|
| 正在执行任务 | state=working / status=busy | 绿色心电图跳动 |
| 交互会话打开但空闲 | kind=interactive 非 busy | 绿色静态 ◉ |
| 等待权限批准 | state=blocked / status=waiting | 橙色 ⚠ 闪烁 |
| 后台待命/排队 | kind=background 非 busy | 橙色 ● |
| 已完成 | state=done（留在 active 列表一段时间！） | 灰点 + 未读 |
| 未运行 | 不在列表 | 灰点 |

关键：**"完成"判定靠 state 从 working → done 的转变，不是"列表消失"**（done 任务会滞留 active 列表）。

### 5.4 favorites.json（与 fyne 版共用格式）

```json
{"ids":[收藏], "aliases":{会话ID:别名}, "hidden":[软删除ID]}
```
- 重命名 = 本地别名，不碰 claude 数据；真正改名用 claude 内 `/rename`。
- 删除 = 软隐藏（不物理删文件），UI 提供「已删」面板恢复。

## 6. 踩坑与修复（按时间线）

| # | 问题 | 根因 | 修复 |
|---|---|---|---|
| 1 | 启动弹 "wails applications will not build..." | `-tags webview2` 缺 `production`，wails 编译进错误框占位实现 | 构建加 `production` tag |
| 2 | WebView2 0x800700AA 起不来 | 沙箱/受限环境（浏览器子进程拉不起来） | 确认只是测试环境问题，真机正常 |
| 3 | 恢复第二个会话闪退 | ConPTY 双重关闭 → 句柄复用冲突 → 堆损坏 0xc0000374 | ptyRef 幂等 Close |
| 4 | 滚动条挡内容、内容溢出 | `.term-host` padding 计入 fit 测量（clientWidth 含 padding）→ 列数偏大 | 留白改透明 border + fit 后让出 1 列 |
| 5 | 关标签要点两次、出现 UUID 幽灵标签 | 关闭瞬间迟到的 term:data 触发兜底 openTab(token) | closedTokens 集合拦截迟到事件 |
| 6 | 打开闪终端、每 10s 闪一次 | 监控子进程 cmd 在 windowsgui 下新开控制台窗口 | exec 加 `CREATE_NO_WINDOW` |
| 7 | 状态监控误判（打开=心跳） | 只看了 kind，没看 state | 实测 schema，state/status 细分语义 |
| 8 | 任务完成无提示（心跳直接变灰） | 完成检测只认 `state=done/消失`；交互会话完成信号是 `status: busy->idle`（无 state 字段），被当成"没变化"忽略；动画收尾还强制标灰 | 检测改为"上一轮忙碌 && 本轮不再忙碌"即结束（覆盖 background working→done 与 interactive busy→idle 两种语义）；动画后按真实状态重绘；提示链 = 心跳停止动画 + 顶部 toast + 状态栏 + 提示音（MessageBeep 系统音 + Beep 三连音，双保险）+ 未读（盯着看不打扰）；agents-debug.log 诊断日志（每次轮询原始 JSON + 前端检测决策） |

## 7. 扩展指南（加一个新功能的标准步骤）

1. **后端**：`app.go` 或新文件加 `func (a *App) Xxx(...) (T, error)`。
   - 多会话操作记得按 token 从 `a.terms` 取，涉及关闭走 `ptyRef.close()`（幂等）。
2. **绑定**：`frontend/wailsjs/go/app/App.js` 加对应 `export function Xxx(...) { return window['go']['app']['App']['Xxx'](...); }`。
3. **前端**：`frontend/src/main.js` 顶部 import；调用即可（返回值是 Promise）。
4. 需要后端主动通知 → `runtime.EventsEmit(a.ctx, "事件名", 参数...)`，前端 `window.runtime.EventsOn(...)`。
5. 重新构建：前端 `npm run build` → 后端 `go build -tags "webview2 production" ...`。

## 7.5 发布新版本（一键更新的发布侧）

- 版本号 = git tag，形如 `v0.2-wails`（正式版）或 `v0.2-wails-rc`（预发布）。
- `git tag v0.2-wails && git push origin v0.2-wails` → CI（wails-build.yml）构建并把
  `claude-terminal.exe` 发布为 GitHub Release，`-X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=v0.2-wails` 自动注入。
- 更新器只认 `v*-wails` 正式版：多版本并存时取语义版本最高者；预发布（GitHub
  `prerelease=true`，CI 按 `-pre/-rc` 后缀标）默认不提示，避免把测试版推给用户。
- 想要更多人"收到更新"，发布新 tag 即可；无需改代码。

## 8. 已知限制

- claude 内 `/theme auto` 识别不到本软件终端的亮色背景：ConPTY 的
  conhost 会拦截 claude 的 OSC 11 背景色查询并回复黑色（查询到不了
  xterm），换亮色主题后 auto 仍判深色。曾试过 OSC 注入与自动下发
  `/theme light`，用户要求不干预 claude 主题，已撤销并记录（run.log
  [34][35][36]）。目前需手动在 claude 内 `/theme light` 或改
  settings.json。
- 终端快捷键：xterm 默认 Ctrl+V 依赖隐藏 textarea 的原生 paste 事件，
  WebView2 下不可靠；Ctrl+Enter 会被 xterm 剥掉修饰符当普通回车。
  已在 attachCustomKeyEventHandler 显式接管（run.log [37]）：
  Ctrl+V/Ctrl+Shift+V/Shift+Insert=粘贴（clipboard API + execCommand
  双保险，走 term.paste() 自动处理 \r\n 与 bracketed paste），
  Ctrl+Enter=发送 LF。wails v2.14.0 已禁用浏览器加速键，Ctrl+R/F 不会抢键。
- UI 日间/夜间模式（run.log [38]）：CSS 全量变量化，:root=夜间、
  html[data-theme="light"]=日间，main.js 的 applyUiTheme() 写
  data-theme + localStorage('ui-theme')，默认日间。设置按钮在侧边栏
  底部左下角，菜单向上展开（按钮贴近视口底部）。终端 8 色配色
  （--term-bg / 🎨 菜单）与 UI 模式完全独立，互不影响。
  日后新增 UI 样式一律用 CSS 变量，禁止硬编码颜色。
- 未读标记是内存态：重启应用后不保留（未持久化"已读/未读"）。
- interactive 会话出现在 agents 列表的精确行为未在真机上长时间验证（设计上已兼容：busy→心跳、idle→静态绿）。
- **一键更新（2026-08-20 新增，详见 7.5 / README「发布与更新」）**：
  - 自更新只做**粗校验**（非空 + MZ 头），未做签名/哈希校验；仅从本仓库 Releases 下载，请勿把
    更新源指向不可信位置。仓库公开时无需 token，GitHub API 未认证限额 60 次/小时（手动检查场景足够）。
  - 更新会**结束当前所有 ConPTY 会话进程**（先持久化清单、新版启动自动恢复），"点完立刻重启生效"
    是预期行为；若希望平滑，可后续改成"下载完成 → 提示下次启动生效"。
  - 自替换利用 Windows「运行中的 exe 只许改名、不许删除」：当前 exe → `.old`，新版落地原名，
    新版启动时删 `.old`/`.new`。若某环境连改名都失败（杀软锁文件），Apply 会回滚并报错，程序照常运行。
  - 目录权限：exe 所在目录需可写（放 Program Files 等受保护目录会更新失败，宜用普通目录）。
- 会话解析曾在 Wails 版与旧 Fyne 版有重复实现；当前仓库只保留 Wails 版。
- 图标：`.syso` 由 `assets/icon.rc` + `assets/icon.ico` 生成（图标设计同 fyne 版，
  不动设计；fyne 版 `.syso` 的组 ID 是 1，**Wails 窗口标题栏图标用
  `winc.AppIconID=3`（internal/frontend/desktop/windows/winc/app.go）按 ID 3 从
  exe 加载**，ID 1 只对资源管理器/任务栏有效，标题栏会回退 Wails 默认图标。
  所以 `icon.rc` 必须是 `3 ICON "assets/icon.ico"`；重新生成：
  在项目根执行 `windres assets/icon.rc -O coff -o rsrc_windows_amd64.syso`。
  改图标后可用 `assets/verify-icon.ps1` 验证：ID 3 资源存在 + 提取像素为设计图。

## 9. 提效提示

- 任何疑似并发/句柄问题：先跑 `go test -run TestPtyRefNoDoubleClose -count=5`。
- favorites 数据链路：`go test -run TestFavAliasAndDelete`。
- 修改前端后必须 `npm run build` 再构建 exe（dist 是嵌入的）。
