# Claude 会话管理 - Fyne 版

跨平台 GUI（Go + Fyne），**单二进制零依赖**：双击 = 侧边栏窗口；`-run <会话ID>` = 终端内恢复器。
逻辑全部用 Go 实现，不依赖 PowerShell 脚本。

## 功能

- 会话目录树（按项目分组，组按目录名排序；组内会话按最近使用时间排序；启动全展开；**双击项目分支 = 折叠/展开**）
- **双击会话** = 恢复（新终端窗口/标签页运行本程序 `-run <id>`：自动 cd、检查活动状态、`claude -r`）；双击 / 点收藏星 / 勾选时该行有按钮同款水波扩散动画
- **运行徽标**：● 后台运行中（橙）/ ● 已打开（绿）/ 后台（灰），每 10s 复查 `claude agents --json`
- **自动刷新**：每 5s 检测会话文件变化（快照签名），有变化才重扫
- **搜索**：按 目录/摘要/ID 过滤（输入即过滤）
- **收藏**：点击每行左侧 ★/☆ 即收藏/取消（金色 ★，存 exe 同目录 `favorites.json`）；「打开收藏」批量恢复
- **多选**：每行勾选框；「打开选中」批量恢复
- **右键菜单**：行上右键 → 打开 / 收藏(取消收藏) / 重命名… / 删除（不再显示）
- **重命名**：右键弹框输入新名称（仅本软件的别名，不修改 claude 数据；真正改名在 claude 会话内用 `/rename`）；清空 = 恢复原名
- **删除**：软隐藏 —— 只把 ID 记入 `favorites.json` 的 hidden 列表，界面不再显示，**不物理删除**会话文件
- **不重复打开**：已打开/后台运行中的会话双击与批量打开时跳过并提示

## 构建

需要 Go 1.21+ 和 C 编译器（Fyne 依赖 CGO；Windows 需 gcc/MinGW）：

```powershell
go mod tidy
go build -ldflags "-s -w" -o claude-sidebar.exe .   # 控制台子系统：GUI 模式自隐藏控制台，-run 模式在终端里跑 claude
```

> 图标说明：exe 文件图标由 `rsrc_windows_amd64.syso` 提供（已提交，构建时自动链接）。
> 修改图标：编辑 `icon.svg` → 运行 `gen-icon.ps1` 生成 `icon.ico` → `windres icon.rc -O coff -o rsrc_windows_amd64.syso`。

macOS / Linux 同样命令各自编译一份（无平台特定代码）。

## 运行

- 双击 `claude-sidebar.exe`（GUI 模式，控制台自动隐藏）
- 终端里手动恢复：`claude-sidebar.exe -run <会话ID>`
- 干跑检查（只检查不执行 claude，调试用）：`claude-sidebar.exe -dry -run <会话ID>`
- 诊断：exe 同目录 `gui.log` 记录每次点击与打开动作及结果

## 已知说明

- Windows 双击会话用 `wt new-tab`（若 exe 路径含空格则退回新控制台窗口）
- macOS/Linux 的终端打开方式为简化实现（osascript / gnome-terminal 等），按需打磨
- 中文字体自动探测系统字体（Fyne 默认字体无 CJK；不支持 TTC 集合）
