package main

// Claude 会话管理 - Fyne 版（跨平台 GUI）
// 功能：会话目录树（双击恢复）、收藏、刷新、自动刷新、运行状态徽标、搜索过滤

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed assets/icon.svg
var iconSVG []byte

var (
	procGetConsoleWindow      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	procGetConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	procShowWindow            = syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow")
)

var (
	colOrange = color.NRGBA{R: 0xFF, G: 0xB3, B: 0x47, A: 0xFF} // ● 后台运行中
	colGreen  = color.NRGBA{R: 0x7F, G: 0xD9, B: 0x7F, A: 0xFF} // ● 已打开
	colGray   = color.NRGBA{R: 0x9E, G: 0x9E, B: 0x9E, A: 0xFF} // 后台
	colGold   = color.NRGBA{R: 0xFF, G: 0xD7, B: 0x00, A: 0xFF} // ★ 收藏
)

// starGlyph / starColor 收藏星外观：★=已收藏（金色），☆=未收藏（灰色）
func starGlyph(fav bool) string {
	if fav {
		return "★"
	}
	return "☆"
}

func starColor(fav bool) color.Color {
	if fav {
		return colGold
	}
	return colGray
}

// pickCJKFont 找系统自带的单字体中文字体（Fyne 不支持 TTC 字体集合，会崩溃；
// 只选 .ttf/.otf 单字体文件）
func pickCJKFont() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Windows\Fonts\simhei.ttf`,  // 黑体
			`C:\Windows\Fonts\simkai.ttf`,  // 楷体
			`C:\Windows\Fonts\simfang.ttf`, // 仿宋
			`C:\Windows\Fonts\Deng.ttf`,    // 等线
		}
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/Library/Fonts/Arial Unicode.ttf",
		}
	default:
		candidates = []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.otf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttf",
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func favPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "favorites.json"
	}
	return filepath.Join(filepath.Dir(exe), "favorites.json")
}

func loadFavorites() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(favPath())
	if err != nil {
		return m
	}
	var j struct {
		Ids []string `json:"ids"`
	}
	if json.Unmarshal(b, &j) == nil {
		for _, id := range j.Ids {
			m[id] = true
		}
	}
	return m
}

func saveState(favs map[string]bool, aliases map[string]string, hidden map[string]bool) {
	ids := make([]string, 0, len(favs))
	for id := range favs {
		ids = append(ids, id)
	}
	hid := make([]string, 0, len(hidden))
	for id := range hidden {
		hid = append(hid, id)
	}
	b, _ := json.Marshal(map[string]any{"ids": ids, "aliases": aliases, "hidden": hid})
	_ = os.WriteFile(favPath(), b, 0o644)
}

// loadHidden 已隐藏（软删除）的会话 ID：只记录不物理删除，界面不再显示
func loadHidden() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(favPath())
	if err != nil {
		return m
	}
	var j struct {
		Hidden []string `json:"hidden"`
	}
	if json.Unmarshal(b, &j) == nil {
		for _, id := range j.Hidden {
			m[id] = true
		}
	}
	return m
}

// loadAliases 本地自定义会话名（别名只存侧边栏自己的 json，不触碰 claude 数据文件）
func loadAliases() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(favPath())
	if err != nil {
		return m
	}
	var j struct {
		Aliases map[string]string `json:"aliases"`
	}
	if json.Unmarshal(b, &j) == nil {
		for k, v := range j.Aliases {
			if strings.TrimSpace(v) != "" {
				m[k] = v
			}
		}
	}
	return m
}

// guiLog 追加日志到 exe 旁的 gui.log（点击/打开动作与结果），排查“点了没反应”用
func guiLog(format string, args ...any) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "gui.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// hideConsole GUI 模式自隐藏控制台（本程序是控制台子系统，双击时会有控制台窗口）。
// 仅当控制台是本进程独占时才隐藏——从终端里启动（共享控制台）不隐藏，避免搞没用户终端。
func hideConsole() {
	if runtime.GOOS != "windows" {
		return
	}
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 4)
	if n > 1 {
		return // 共享控制台
	}
	procShowWindow.Call(hwnd, 0) // SW_HIDE
}

// ---- 行内图标按钮（收藏星 ★ / 新建会话 +）：文字图标 + 点击动作 + 按压变亮 ----

type rowIcon struct {
	widget.BaseWidget
	text    *canvas.Text
	onTap   func()
	pressed bool
}

func newRowIcon(s string, c color.Color) *rowIcon {
	i := &rowIcon{}
	i.text = canvas.NewText(s, c)
	i.text.TextSize = 17
	i.ExtendBaseWidget(i)
	return i
}

// set 由 UpdateNode 调用：绑定当前行的图标文字/颜色/点击动作（树的行对象会被复用）
func (i *rowIcon) set(s string, c color.Color, onTap func()) {
	i.text.Text = s
	i.text.Color = c
	i.onTap = onTap
	i.apply()
}

func (i *rowIcon) apply() {
	if i.pressed {
		i.text.Color = color.White // 按下变亮，同按钮按压手感
	}
	i.text.Refresh()
}

func (i *rowIcon) Tapped(*fyne.PointEvent) {
	if i.onTap != nil {
		i.onTap()
	}
}

func (i *rowIcon) MouseDown(*desktop.MouseEvent) {
	i.pressed = true
	i.apply()
}

func (i *rowIcon) MouseUp(*desktop.MouseEvent) {
	i.pressed = false
	i.apply()
}

func (i *rowIcon) CreateRenderer() fyne.WidgetRenderer {
	return &rowIconRenderer{i: i}
}

type rowIconRenderer struct {
	i *rowIcon
}

func (r *rowIconRenderer) Destroy() {}

func (r *rowIconRenderer) Layout(sz fyne.Size) {
	r.i.text.Move(fyne.NewPos(3, 2))
	r.i.text.Resize(r.i.text.MinSize())
}

func (r *rowIconRenderer) MinSize() fyne.Size {
	return r.i.text.MinSize().Add(fyne.NewSize(6, 4)) // 命中区域略大于字形，更好点
}

func (r *rowIconRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.i.text}
}

func (r *rowIconRenderer) Refresh() {
	r.i.apply()
}

// ---- 悬浮提示层：自绘 tooltip ----
// Fyne 2.8 无内置 Tooltip；PopUp 的 OverlayContainer 是全画布 Hoverable，会抢走
// hover 事件导致提示"先关再开"闪烁且不跟手。改用内容层顶部的自绘层：
// 不实现任何事件接口（不参与命中测试），移动时只改坐标并重绘，平滑跟随。

type tipLayer struct {
	widget.BaseWidget
	bg   *canvas.Rectangle
	text *canvas.Text
	pos  fyne.Position
	size fyne.Size
	show bool
}

func newTipLayer() *tipLayer {
	th := fyne.CurrentApp().Settings().Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	t := &tipLayer{}
	t.bg = canvas.NewRectangle(th.Color(theme.ColorNameOverlayBackground, v))
	t.bg.CornerRadius = 6
	t.text = canvas.NewText("", th.Color(theme.ColorNameForeground, v))
	t.text.TextSize = 13
	t.bg.Hide()
	t.text.Hide()
	t.ExtendBaseWidget(t)
	return t
}

// showAt 在 pos 处显示文本，自动收进 bounds（窗口画布）内
func (t *tipLayer) showAt(pos fyne.Position, text string, bounds fyne.Size) {
	t.text.Text = text
	ms := t.text.MinSize().Add(fyne.NewSize(16, 10)) // 内边距
	pos = pos.Add(fyne.NewPos(12, 20))               // 略微偏移鼠标，避免遮住指针
	if pos.X+ms.Width > bounds.Width {
		pos.X = bounds.Width - ms.Width - 4
	}
	if pos.Y+ms.Height > bounds.Height {
		pos.Y = bounds.Height - ms.Height - 4
	}
	if pos.X < 0 {
		pos.X = 0
	}
	if pos.Y < 0 {
		pos.Y = 0
	}
	t.pos = pos
	t.size = ms
	t.show = true
	t.bg.Show()
	t.text.Show()
	t.text.Refresh()
	t.Refresh()
}

func (t *tipLayer) hide() {
	if !t.show {
		return
	}
	t.show = false
	t.bg.Hide()
	t.text.Hide()
	t.Refresh()
}

func (t *tipLayer) CreateRenderer() fyne.WidgetRenderer {
	return &tipLayerRenderer{t: t}
}

type tipLayerRenderer struct {
	t *tipLayer
}

func (r *tipLayerRenderer) Destroy() {}

// Layout 忽略传入的布局尺寸：位置/大小完全由 showAt 指定（手动定位）
func (r *tipLayerRenderer) Layout(sz fyne.Size) {
	r.t.bg.Move(r.t.pos)
	r.t.bg.Resize(r.t.size)
	r.t.text.Move(r.t.pos.Add(fyne.NewPos(8, 5)))
	r.t.text.Resize(r.t.text.MinSize())
}

func (r *tipLayerRenderer) MinSize() fyne.Size { return fyne.NewSize(1, 1) }

func (r *tipLayerRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.t.bg, r.t.text}
}

func (r *tipLayerRenderer) Refresh() { r.Layout(r.t.Size()) }

// ---- 行内水波反馈：复刻 Fyne 按钮的 tap 动画 ----
// 与 widget/button.go 的 newButtonTapAnimation 同款：圆角矩形从行水平中点向两侧
// 扩展，按压色随进度淡出，EaseOut 曲线，时长 300ms（canvas.DurationStandard）。
// 只在“确认动作”时播放：会话双击恢复、项目分支双击折叠/展开、点收藏星、勾选。
// 单击选中不播水波。

type rowBody struct {
	widget.BaseWidget
	content   *fyne.Container // HBox：收藏星/勾选框/新建+/文本/徽标
	tapBG     *canvas.Rectangle
	tapAnim   *fyne.Animation
	suppress  bool            // SetChecked 同步勾选状态时抑制 OnChanged 副作用（行对象复用）
	onTap     func(*fyne.PointEvent) // 左键回调（转发给树：选中/双击）
	onMenu    func(*fyne.PointEvent) // 右键回调（UpdateNode 绑定当前行会话）
	onTip     func(fyne.Position)    // 悬浮提示回调（UpdateNode 绑定当前行，nil=无提示）
	onTipHide func()                 // 移出时隐藏提示
}

func newRowBody(content *fyne.Container) *rowBody {
	r := &rowBody{content: content}
	r.tapBG = canvas.NewRectangle(color.Transparent)

	th := fyne.CurrentApp().Settings().Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	r.tapAnim = fyne.NewAnimation(300*time.Millisecond, func(done float32) {
		w := r.Size().Width
		h := r.Size().Height
		if w <= 0 {
			return
		}
		mid := w / 2
		size := mid * done
		r.tapBG.Resize(fyne.NewSize(size*2, h))
		r.tapBG.Move(fyne.NewPos(mid-size, 0))
		c := color.NRGBAModel.Convert(th.Color(theme.ColorNamePressed, v)).(color.NRGBA)
		fade := c.A - uint8(float32(c.A)*done)
		if fade > 0 {
			r.tapBG.FillColor = color.NRGBA{R: c.R, G: c.G, B: c.B, A: fade}
		} else {
			r.tapBG.FillColor = color.Transparent
		}
		r.tapBG.CornerRadius = h / 2
		r.tapBG.Refresh()
	})
	r.tapAnim.Curve = fyne.AnimationEaseOut
	r.ExtendBaseWidget(r)
	return r
}

// tapAnimation 播放水波：仅在双击 / 收藏星 / 勾选等确认动作时调用
// （与按钮相同：停止上一次、重新开始；尊重系统“关闭动画”设置）
func (r *rowBody) tapAnimation() {
	if r.tapAnim == nil {
		return
	}
	r.tapAnim.Stop()
	if fyne.CurrentApp().Settings().ShowAnimations() {
		r.tapAnim.Start()
	}
}

// Tapped 左键：rowBody 比 treeNode 更深，命中测试优先命中这里。
// 原样复刻 treeNode.Tapped 的行为：选中 + 键盘焦点，双击检测仍走 OnSelected 的 350ms 逻辑。
func (r *rowBody) Tapped(ev *fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap(ev)
	}
}

// TappedSecondary 右键：弹出该行的会话菜单（treeNode 不处理右键，事件直达这里）
func (r *rowBody) TappedSecondary(ev *fyne.PointEvent) {
	if r.onMenu != nil {
		r.onMenu(ev)
	}
}

// ---- 悬浮提示（Fyne 2.8 无内置 Tooltip，手写）：项目行悬浮显示完整路径 ----

func (r *rowBody) MouseIn(ev *desktop.MouseEvent) {
	if r.onTip != nil {
		r.onTip(ev.AbsolutePosition)
	}
}

func (r *rowBody) MouseMoved(ev *desktop.MouseEvent) {
	if r.onTip != nil {
		r.onTip(ev.AbsolutePosition)
	}
}

func (r *rowBody) MouseOut() {
	if r.onTipHide != nil {
		r.onTipHide()
	}
}

func (r *rowBody) CreateRenderer() fyne.WidgetRenderer {
	return &rowBodyRenderer{r: r}
}

type rowBodyRenderer struct {
	r *rowBody
}

func (rr *rowBodyRenderer) Destroy() {}

func (rr *rowBodyRenderer) Layout(sz fyne.Size) {
	rr.r.content.Resize(rr.r.content.MinSize())
	rr.r.content.Move(fyne.NewPos(0, 0))
}

func (rr *rowBodyRenderer) MinSize() fyne.Size { return rr.r.content.MinSize() }

func (rr *rowBodyRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.r.tapBG, rr.r.content}
}

func (rr *rowBodyRenderer) Refresh() {}

func init() {
	// 双击启动（无参数 = GUI 模式）时尽早隐藏自己的控制台窗口，把闪动压到最小；
	// 带参数（-run/-dry 恢复模式）不隐藏。共享控制台（从终端启动）不会被隐藏。
	// 之后 cmd /c claude ... 等子进程会继承这个隐藏控制台，不会再弹出新窗口。
	if len(os.Args) <= 1 {
		hideConsole()
	}
}

func main() {
	// -run <会话ID> = 恢复模式（终端内运行，由 openSession 启动）；无参数 = GUI 模式
	// -new <目录> = 新建会话模式（在指定目录启动全新 claude 会话）
	runID := flag.String("run", "", "恢复模式：直接恢复指定会话（在终端内运行）")
	newDir := flag.String("new", "", "新建模式：在指定目录启动全新 claude 会话")
	dry := flag.Bool("dry", false, "恢复/新建模式干跑：只做检查不实际执行 claude（调试用）")
	flag.Parse()
	if *runID != "" {
		_ = runSession(*runID, *dry)
		return
	}
	if *newDir != "" {
		_ = runNew(*newDir, *dry)
		return
	}

	// 中文字体：Fyne 默认字体不含 CJK 字形，必须在 app 创建前设置 FYNE_FONT
	if f := pickCJKFont(); f != "" {
		os.Setenv("FYNE_FONT", f)
	}
	a := app.NewWithID("com.claude.sidebar")
	// 声明已迁移到 Fyne 2.8 的 fyne.Do 线程模型（消除启动警告）。
	// 本程序所有 goroutine 的 UI 更新均已通过 fyne.Do 投递，符合新模型要求。
	a.Metadata().Migrations["fyneDo"] = true
	a.SetIcon(fyne.NewStaticResource("icon.svg", iconSVG)) // 窗口/任务栏图标
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("Claude 会话管理")
	w.Resize(fyne.NewSize(720, 760))

	favs := loadFavorites()
	aliases := loadAliases()
	hidden := loadHidden()
	var allProjects []*project          // 完整数据
	projects := []*project{}            // 过滤后的视图
	agents := map[string]string{}       // sessionId -> kind（运行状态）
	projByUID := map[string]*project{}
	sessByUID := map[string]*Session{}
	var tree *widget.Tree
	checked := map[string]bool{}      // 多选（勾选框）状态：sessionID -> 是否勾选
	rowByUID := map[string]*rowBody{} // uid -> 当前可见行对象（双击时对对应行播水波）
	leafCount := map[string]int{}     // 项目末端目录名 -> 出现次数（同名时组名消歧用）
	fileSig := ""

	// rippleUID 对指定行播放水波（同工具栏按钮的 tap 动画）
	rippleUID := func(uid string) {
		if rb, ok := rowByUID[uid]; ok {
			rb.tapAnimation()
		}
	}

	status := widget.NewLabel("加载中…")
	search := widget.NewEntry()
	search.SetPlaceHolder("搜索 目录 / 摘要 / ID")

	// 底部状态行：会话/项目/收藏/勾选计数，任何变化都实时刷新
	updateStatus := func() {
		total := 0
		for _, p := range projects {
			total += len(p.Sessions)
		}
		shown := ""
		if strings.TrimSpace(search.Text) != "" {
			shown = fmt.Sprintf(" · 显示 %d", total)
		}
		status.SetText(fmt.Sprintf("%d 个会话 · %d 个项目%s · 双击恢复 · 收藏 %d · 勾选 %d", total, len(projects), shown, len(favs), len(checked)))
	}

	// showName 显示名：本地别名优先，否则会话标题/摘要
	showName := func(s *Session) string {
		if n, ok := aliases[s.ID]; ok && strings.TrimSpace(n) != "" {
			return n
		}
		return displayName(s)
	}

	// 扫描磁盘（有变化才真正重扫），返回是否有变化
	refreshData := func() bool {
		sig := fileSignature()
		if sig == fileSig {
			return false
		}
		fileSig = sig
		list := scanAll()
		vis := list[:0] // 过滤已隐藏（软删除）的会话
		for _, s := range list {
			if !hidden[s.ID] {
				vis = append(vis, s)
			}
		}
		allProjects = groupSessions(vis)
		return true
	}

	// 应用过滤 + 重建树
	applyFilter := func() {
		projByUID = map[string]*project{}
		sessByUID = map[string]*Session{}
		q := strings.ToLower(strings.TrimSpace(search.Text))
		if q == "" {
			projects = allProjects
		} else {
			var list []*Session
			for _, p := range allProjects {
				for i := range p.Sessions {
					s := &p.Sessions[i]
					hay := s.Dir + " " + s.Text + " " + s.ID
					if n, ok := aliases[s.ID]; ok {
						hay += " " + n // 别名也参与搜索
					}
					if strings.Contains(strings.ToLower(hay), q) {
						list = append(list, s)
					}
				}
			}
			projects = groupSessions(list)
		}
		for _, p := range projects {
			projByUID["p:"+p.Name] = p
			for i := range p.Sessions {
				sessByUID["s:"+p.Sessions[i].ID] = &p.Sessions[i]
			}
		}
		leafCount = map[string]int{} // 组名消歧：统计同名末端目录
		for _, p := range projects {
			leafCount[filepath.Base(p.Name)]++
		}
		total := 0
		for _, p := range projects {
			total += len(p.Sessions)
		}
		updateStatus()
		if tree != nil {
			tree.OpenAllBranches()
			tree.Refresh()
		}
		if os.Getenv("FYNE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "FYNE_DEBUG: sessions=%d projects=%d\n", total, len(projects))
		}
	}

	// 全量刷新（手动按钮 / 启动）
	reload := func() {
		refreshData()
		applyFilter()
	}

	// forceReload 强制重扫并重建视图：隐藏/恢复隐藏等界面状态变化时，
	// 会话文件本身没变（fileSignature 相同），必须绕过"无变化就跳过"的优化
	forceReload := func() {
		fileSig = ""
		refreshData()
		applyFilter()
	}

	// ---- 会话操作（右键菜单与双击/星星共用）----

	// openOne 恢复会话：已打开/后台运行中的不重复打开
	openOne := func(s *Session) {
		switch agents[s.ID] {
		case "interactive":
			status.SetText("该会话已打开，未重复打开")
		case "background":
			status.SetText("该会话后台运行中，未重复打开")
		default:
			if err := openSession(s); err != nil {
				status.SetText("打开失败: " + err.Error())
			} else {
				status.SetText("已恢复: " + showName(s))
			}
		}
	}

	// toggleFav 收藏切换（星星与右键菜单共用）
	toggleFav := func(s *Session) {
		if favs[s.ID] {
			delete(favs, s.ID)
		} else {
			favs[s.ID] = true
		}
		saveState(favs, aliases, hidden)
		guiLog("fav toggle id=%s now=%v", s.ID, favs[s.ID])
		if tree != nil {
			tree.Refresh()
		}
		updateStatus()
	}

	menuCanvas := w.Canvas()

	// 悬浮提示（项目行显示完整路径）：自绘 tipLayer 挂在内容层顶部（见 tipLayer 说明）
	tip := newTipLayer()
	showTip := func(pos fyne.Position, text string) {
		tip.showAt(pos, text, w.Canvas().Size())
	}

	// newSession 在指定目录启动全新 claude 会话（-new 模式）
	newSession := func(dir string) {
		if err := openNewSession(dir); err != nil {
			status.SetText("新建会话失败: " + err.Error())
		} else {
			guiLog("new session dir=%s", dir)
			status.SetText("已新建会话: " + filepath.Base(dir))
		}
	}

	// applyRename 应用别名（只改本软件的显示名，不修改 claude 数据）
	applyRename := func(s *Session, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			delete(aliases, s.ID)
			status.SetText("已恢复原名: " + displayName(s))
		} else {
			aliases[s.ID] = name
			status.SetText("已重命名: " + name)
		}
		saveState(favs, aliases, hidden)
		guiLog("rename id=%s name=%q", s.ID, name)
		if tree != nil {
			tree.Refresh()
		}
		updateStatus()
	}

	// showRenamePopup 重命名弹框：提示这里只改别名，真正改名用 claude 内的 /rename
	showRenamePopup := func(s *Session) {
		entry := widget.NewEntry()
		cur := displayName(s)
		if n, ok := aliases[s.ID]; ok {
			cur = n
		}
		entry.SetText(cur)

		hint := widget.NewLabel("这里只修改本软件的别名；真正重命名请在 claude 会话内使用 /rename")
		hint.Wrapping = fyne.TextWrapWord

		var popup *widget.PopUp
		content := container.NewVBox(
			hint,
			entry,
			container.NewHBox(
				widget.NewButton("取消", func() { popup.Hide() }),
				widget.NewButton("确定", func() {
					applyRename(s, entry.Text)
					popup.Hide()
				}),
			),
		)
		card := widget.NewCard("重命名会话", "", content)
		popup = widget.NewModalPopUp(card, menuCanvas)
		sz := card.MinSize()
		cs := menuCanvas.Size()
		popup.ShowAtPosition(fyne.NewPos((cs.Width-sz.Width)/2, (cs.Height-sz.Height)/2))
		entry.OnSubmitted = func(string) {
			applyRename(s, entry.Text)
			popup.Hide()
		}
		menuCanvas.Focus(entry)
	}

	// hideSession 软删除：记录 ID 后界面不再显示（不物理删除会话文件）
	hideSession := func(s *Session) {
		hidden[s.ID] = true
		delete(checked, s.ID)
		saveState(favs, aliases, hidden)
		guiLog("hide id=%s", s.ID)
		forceReload() // 文件未变，必须强制重扫才能立即从列表消失
		status.SetText("已隐藏（不再显示）: " + showName(s))
	}

	// showMenu 行右键菜单：打开 / 收藏 / 重命名 / 删除（软隐藏）
	showMenu := func(s *Session, ev *fyne.PointEvent) {
		favLabel := "收藏"
		if favs[s.ID] {
			favLabel = "取消收藏"
		}
		items := []*fyne.MenuItem{
			fyne.NewMenuItem("打开", func() { openOne(s) }),
			fyne.NewMenuItem(favLabel, func() {
				toggleFav(s)
				rippleUID("s:" + s.ID)
			}),
			fyne.NewMenuItem("重命名…", func() { showRenamePopup(s) }),
			fyne.NewMenuItem("删除（不再显示）", func() { hideSession(s) }),
		}
		widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), menuCanvas, ev.AbsolutePosition)
	}

	tree = &widget.Tree{
		Root: "",
		ChildUIDs: func(uid string) []string {
			if uid == "" {
				ids := make([]string, 0, len(projects))
				for _, p := range projects {
					ids = append(ids, "p:"+p.Name)
				}
				return ids
			}
			if p, ok := projByUID[uid]; ok {
				ids := make([]string, 0, len(p.Sessions))
				for i := range p.Sessions {
					ids = append(ids, "s:"+p.Sessions[i].ID)
				}
				return ids
			}
			return nil
		},
		IsBranch: func(uid string) bool {
			// 根节点 "" 必须视为分支，否则 Fyne 的 walk 不会遍历任何子节点（树渲染为空）
			if uid == "" {
				return true
			}
			_, ok := projByUID[uid]
			return ok
		},
		CreateNode: func(bool) fyne.CanvasObject {
			star := newRowIcon("☆", colGray) // 可点收藏星
			plus := newRowIcon("+", colGray) // 项目行：在此目录新建会话
			cb := widget.NewCheck("", nil)   // 多选勾选框
			main := widget.NewLabel("…")
			badge := canvas.NewText("", color.White)
			return newRowBody(container.NewHBox(star, cb, plus, main, badge))
		},
		UpdateNode: func(uid string, branch bool, obj fyne.CanvasObject) {
			rb := obj.(*rowBody)
			hb := rb.content
			rowByUID[uid] = rb
			rb.onTipHide = func() { tip.hide() } // 行被复用/移出时收起悬浮提示
			// 行体接管左键：转发选中给树（保持单击选中/双击恢复/双击分支折叠）
			rb.onTap = func(*fyne.PointEvent) {
				if tree == nil {
					return
				}
				tree.Select(uid)
				if c := fyne.CurrentApp().Driver().CanvasForObject(tree); c != nil && c.Focused() != tree {
					if !fyne.CurrentDevice().IsMobile() {
						c.Focus(tree)
					}
				}
			}
			star := hb.Objects[0].(*rowIcon)
			cb := hb.Objects[1].(*widget.Check)
			plus := hb.Objects[2].(*rowIcon)
			main := hb.Objects[3].(*widget.Label)
			badge := hb.Objects[4].(*canvas.Text)
			if p, ok := projByUID[uid]; ok {
				star.Hide()
				cb.Hide()
				plus.Show()
				plus.set("+", colGray, func() { newSession(p.Name) })
				rb.onMenu = nil
				rb.onTip = func(pos fyne.Position) { showTip(pos, p.Name) }
				main.SetText(fmt.Sprintf("%s  (%d)", leafLabel(p.Name, leafCount), len(p.Sessions)))
				main.TextStyle = fyne.TextStyle{Bold: true}
				badge.Text = ""
			} else if s, ok := sessByUID[uid]; ok {
				star.Show()
				cb.Show()
				plus.Hide()
				rb.onTip = nil
				rb.onMenu = func(ev *fyne.PointEvent) { showMenu(s, ev) }
				id := s.ID
				var tap func()
				tap = func() {
					toggleFav(s) // 状态/保存/整树刷新/计数
					star.set(starGlyph(favs[id]), starColor(favs[id]), tap)
					rb.tapAnimation() // 收藏确认动作 → 水波
				}
				star.set(starGlyph(favs[id]), starColor(favs[id]), tap)
				cb.OnChanged = func(v bool) {
					if v {
						checked[id] = true
					} else {
						delete(checked, id)
					}
					if rb.suppress {
						return // 行对象复用导致的勾选同步，非用户点击
					}
					guiLog("check id=%s v=%v total=%d", id, v, len(checked))
					updateStatus()    // 底部“勾选 N”实时更新
					rb.tapAnimation() // 勾选确认动作 → 水波
				}
				rb.suppress = true
				cb.SetChecked(checked[id])
				rb.suppress = false
				main.SetText(showName(s) + "   " + s.Time.Format("01-02 15:04"))
				main.TextStyle = fyne.TextStyle{}
				switch agents[s.ID] {
				case "background":
					badge.Text = " ● 后台运行中"
					badge.Color = colOrange
				case "interactive":
					badge.Text = " ● 已打开"
					badge.Color = colGreen
				default:
					if s.IsSide {
						badge.Text = "  后台"
						badge.Color = colGray
					} else {
						badge.Text = ""
					}
				}
			}
			badge.Refresh()
		},
	}

	// 双击检测：350ms 内再次点击同一节点 = 双击。
	// 关键：Fyne Tree 对“同一节点”的重复点击不会再次触发 OnSelected（Select 里
	// uid 未变化直接 return），所以每次回调后立即 Unselect 清空选中态，让下一次
	// 点击重新构成“选中变化”。行高亮（currentHighlight）不受影响，视觉反馈保留。
	// 行为：叶子双击 = 恢复会话；项目分支双击 = 折叠/展开；双击均有水波动画。
	var lastUID string
	var lastAt time.Time
	tree.OnSelected = func(uid string) {
		if tree != nil {
			tree.Unselect(uid)
		}
		dbl := uid == lastUID && time.Since(lastAt) < 350*time.Millisecond
		lastUID = uid
		lastAt = time.Now()

		// 项目分支：双击折叠/展开
		if _, isBranch := projByUID[uid]; isBranch {
			if dbl {
				guiLog("dblclick branch uid=%s toggle", uid)
				tree.ToggleBranch(uid)
				rippleUID(uid) // 折叠/展开确认动作 → 水波
			}
			return
		}
		s, ok := sessByUID[uid]
		if !ok {
			return
		}
		guiLog("click uid=%s double=%v", uid, dbl)
		if !dbl {
			return
		}
		rippleUID(uid) // 双击恢复确认动作 → 水波
		openOne(s)
	}

	btnRefresh := widget.NewButton("刷新", reload)

	// 多选操作：勾选后批量打开 / 批量收藏（收藏也可直接点每行的 ★）
	btnOpenChecked := widget.NewButton("打开选中", func() {
		n, skipped := 0, 0
		for _, p := range projects {
			for i := range p.Sessions {
				id := p.Sessions[i].ID
				if !checked[id] {
					continue
				}
				if agents[id] != "" {
					skipped++
					continue
				}
				if err := openSession(&p.Sessions[i]); err == nil {
					n++
				}
			}
		}
		status.SetText(fmt.Sprintf("已打开 %d 个勾选会话（跳过 %d 个已打开/运行中的）", n, skipped))
	})

	btnOpenFav := widget.NewButton("打开收藏", func() {
		n := 0
		skipped := 0
		for _, p := range projects {
			for i := range p.Sessions {
				if favs[p.Sessions[i].ID] {
					if agents[p.Sessions[i].ID] != "" {
						skipped++
						continue
					}
					if err := openSession(&p.Sessions[i]); err == nil {
						n++
					}
				}
			}
		}
		if skipped > 0 {
			status.SetText(fmt.Sprintf("已打开 %d 个收藏会话（跳过 %d 个已打开的）", n, skipped))
		} else {
			status.SetText(fmt.Sprintf("已打开 %d 个收藏会话", n))
		}
	})

	// 设置：开机自启动
	btnSettings := widget.NewButton("设置", func() {
		applying := false
		var cb *widget.Check
		cb = widget.NewCheck("开机自启动（登录后自动在后台运行）", func(v bool) {
			if applying {
				return // 回滚 SetChecked 引起的递归
			}
			applying = true
			if err := setAutostart(v); err != nil {
				status.SetText("开机自启动设置失败: " + err.Error())
				cb.SetChecked(!v)
			} else {
				guiLog("autostart set=%v", v)
				if v {
					status.SetText("已开启开机自启动")
				} else {
					status.SetText("已关闭开机自启动")
				}
			}
			applying = false
		})
		cb.SetChecked(isAutostart())

		var popup *widget.PopUp
		content := container.NewVBox(
			cb,
			container.NewHBox(widget.NewButton("关闭", func() { popup.Hide() })),
		)
		card := widget.NewCard("设置", "", content)
		popup = widget.NewModalPopUp(card, menuCanvas)
		sz := card.MinSize()
		cs := menuCanvas.Size()
		popup.ShowAtPosition(fyne.NewPos((cs.Width-sz.Width)/2, (cs.Height-sz.Height)/2))
	})

	search.OnChanged = func(string) { applyFilter() }

	btnBox := container.NewHBox(btnRefresh, btnOpenChecked, btnOpenFav, btnSettings)
	top := container.NewBorder(nil, nil, nil, btnBox, search)
	// 悬浮提示层叠在最上层（不拦截任何事件），主界面在下面
	w.SetContent(container.NewStack(container.NewBorder(top, status, nil, nil, tree), tip))
	reload()

	// 自动刷新：5s 检测文件变化（有变化才重扫）
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			fyne.Do(func() {
				if refreshData() {
					applyFilter()
				}
			})
		}
	}()

	// 运行状态：启动查一次 + 每 10s 复查 claude agents --json
	go func() {
		check := func() {
			m := fetchAgents()
			if m == nil {
				return
			}
			fyne.Do(func() {
				if !mapsEqual(agents, m) {
					agents = m
					if tree != nil {
						tree.Refresh()
					}
				}
			})
		}
		check()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			check()
		}
	}()

	w.ShowAndRun()
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
