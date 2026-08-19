# Claude 会话管理

Wails 内嵌终端版：**左侧会话栏 + 右侧真终端**。每个 Claude Code 会话在后台常驻，可同时开多个、来回切换不中断，方便多会话一起用。

## 主要功能

- **内嵌真终端**：xterm.js + ConPTY，方向键、`/` 命令、交互菜单都原生支持
- **左侧会话栏**：按项目分组展示，当前终端对应行高亮；行尾 × 结束会话，右键可重命名 / 归档（软隐藏，不删数据）
- **多会话**：点击会话恢复、点分组行 `+` 新建，切换互不关闭
- **运行状态**：会话行徽标实时显示运行状态，任务完成有提示音 + 横幅 + 未读标记
- **外观**：日间 / 夜间模式 + 8 种终端配色（左下角 ⚙ 设置）
- **快捷键**：`Ctrl+V` 粘贴、`Ctrl+Enter` 换行而不提交

## 使用

从 [Releases](https://github.com/gp-alex-chen/claude-session-manager/releases) 下载 `claude-terminal.exe`（Windows，需 WebView2 运行时），双击即可。

## 开发

Go（Wails v2）+ xterm.js + ConPTY，代码在 `wails-terminal/`。目录结构、构建与维护经验见 [`wails-terminal/README.md`](wails-terminal/README.md)。

> 另有 Fyne 侧边栏版（无内嵌终端），源码在 `src/`。