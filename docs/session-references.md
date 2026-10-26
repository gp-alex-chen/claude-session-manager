# 拖入会话引用

TheOne 在新建的 Codex 会话中通过官方 app-server 动态工具接口提供会话读取与消息发送，保留原生 CLI 输入区。当前验证版本为 Codex CLI 0.162.0。TheOne 不再启动会话 MCP 服务，不注入 `mcp_servers`、MCP 凭据或远程目录检查；用户自己配置的 MCP 继续由 Codex 管理。

## 使用方法

1. 更新到支持原生会话工具的版本，并重新启动 TheOne。
2. 在 TheOne 中新建一个 Codex 会话 A，等待原生输入区就绪。
3. 从侧栏把已有会话 B 拖入 A 的终端正文。松开后，`[@会话名称](thread://会话UUID)` 插入 A 的草稿，不会自动发送或清空原输入。
4. 明确输入指令，例如「读取这个会话最近的结论」，或「给这个会话发送：请补充测试结果」，再自行提交。

窗格标题栏的 Agent 图标拖动仍用于交换分屏位置；会话引用从侧栏的会话行拖起。

## 原生工具入口

TheOne 在用户会话的 `thread/start.dynamicTools` 中注册固定命名空间 `theone_threads`。模型调用时，Codex 发出官方 `item/tool/call`，TheOne 直接通过该原生连接返回结果。它们是 TheOne 实现、由 Codex 原生动态工具协议承载的功能，不伪装成 Codex 内置工具。

| 工具 | 功能 |
|---|---|
| `list_references()` | 只列出当前终端被明确拖入授权的会话 |
| `read_thread(reference, cursor?)` | 通过官方 `thread/turns/list` 分页读取已保存的用户、助手文本，每页五个轮次 |
| `send_message_to_thread(reference, message)` | 按用户明确指令，通过目标已有连接的官方 `turn/start` 发送消息 |

`reference` 接受已授权 UUID、`thread://UUID` 链接或内部引用标识。仅粘贴没有授权的链接不会授予本组工具访问权限。历史文字是任务参考资料，不是新的系统指令；读取不返回隐藏推理、图片或命令执行输出，单页有长度上限。运行中尚未保存的文字可能不可见。

读取目标可以关闭。发送目标必须在当前 TheOne 中有且只有一个在线终端，并且空闲；运行、等待审批、切换身份或断线时会拒绝。目标原生终端会显示消息及后续回答，其未提交草稿继续保留。`accepted` 只表示输入已接收，不代表任务完成；超时或断线后的结果可能未知，不得自动重发。

收到消息不会自动建立反向引用，也不是自动回复的授权。本组工具的处理宿主是 TheOne，需要在 TheOne 中使用；直接从普通 cmd 恢复会话时，没有 TheOne 宿主来执行这些动态调用。

## 新旧会话与恢复

- Codex 0.162.0 只允许在新建会话时注册动态工具；给旧会话的 `thread/resume` 传入新工具会被忽略。
- 因此，旧版创建的会话继续保留原 UUID 和历史，可以作为读取或发送目标；需要使用本组工具时，在新建会话中引用它。TheOne 不复制会话、不修改原始历史或私有数据库。
- 使用本版原生工具创建并保存过的会话，可以在恢复后继续使用原生工具。TheOne 的 `codex-native-tools.json` 只记录会话 UUID 与工具 catalog 版本，用于确认能力，不包含引用授权、目标、runtime token 或凭据。
- 引用授权只在内存中，绑定当前终端、服务代次和会话身份 epoch。关闭、断线、重新打开或切换用户会话后需要重新拖入；即使切回相同 UUID，旧授权也不会恢复。
- 原生 CLI 的标题生成等临时任务不会被当作用户会话切换，不会撤销当前引用。
- 删除能力缓存或缓存保存失败后，恢复会话可能无法被确认支持本组工具；可从新建会话引用旧会话继续工作。

## 实现与验证

共享 app-server 仍由 TheOne 生命周期管理，各终端使用独立连接。只有托管连接的初始化、新建和首次恢复请求会补充所需能力或项目目录；其余协议字段、用户工具和审批保留原行为。仅 TheOne 自有命名空间的动态调用由宿主处理，其他工具和审批继续交给原生 TUI。

回归测试使用临时 CODEX_HOME、合成凭据与本地 Responses 服务，不读取真实用户会话，也不调用外部模型：

```powershell
go test ./internal/provider ./internal/app ./internal/state
go test -tags integration ./internal/provider -run 'TestCodexReferenceNativeDeliveryIntegration|TestCodexNativeTools|TestNativeToolSends|TestCodexShared' -count=1 -timeout=120s
```

验证范围包括原生工具实际调用、读取结果、双终端消息显示、目标草稿保留、新旧会话兼容、工具 catalog 持久化、引用撤权、临时标题任务、项目目录、审批隔离与共享服务恢复。正式构建不启用应用调试日志。
