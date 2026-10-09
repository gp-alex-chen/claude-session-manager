# Agent 产品图标

会话来源图标内置在 `frontend/src/sessions/provider-icons.js`，离线可用。

- **Claude**：取自 [Claude 官方网站](https://claude.com/) 的 Claude wordmark 中独立星形符号；保留原始路径、比例及橙色 `#D97757`。图标属于 Anthropic。
- **Codex**：取自 OpenAI 官方 Windows 应用包 `OpenAI.Codex` 版本 `26.1002.7124.0` 的 `webview/assets/codex_new-f14177b03534.svg`。保留原始路径和比例，单色使用 `currentColor` 适配应用主题。图标属于 OpenAI，参见 [OpenAI 品牌指引](https://openai.com/brand/)。

这些标志仅用于识别会话所属产品。新增 provider 若没有内置图标，则显示其名称。

普通会话行将产品来源与打开状态合并到左侧：未打开使用灰色产品图标，
已打开/任务状态未知使用有色产品图标，Claude 为橙色 `#D97757`，Codex 为蓝色
`#3B82F6`。状态图标使用 `currentColor`，右侧重复来源图标已移除，18px 固定占位
避免切换到执行波形、警告、完成或未读动画时文字跳动。任务判断与其他状态的
符号、颜色、动画保持原有行为；悬停和可访问名称包含产品、状态及临时终端提示。
没有内置图标的 provider 在状态位置使用原来的圆点，并通过提示显示名称。
顶部筛选与归档列表仍使用原来的独立产品图标。
