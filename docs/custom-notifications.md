# 自定义通知程序

在「设置 → 通知」选择事件并配置程序。支持 Python、Go exe、Node.js、PowerShell 等任何可读取标准输入的程序。默认关闭；保存后立即生效，配置在应用数据目录的 `notification.json` 中保存。

这份文档同时面向使用者和通知程序开发者。最常见的接入流程是：

```text
TheOne 检测到状态变化
    → 后台启动用户指定的程序
    → 将事件 JSON 写入程序的标准输入
    → 用户程序转发到本地服务器或其他通知渠道
```

## 快速开始：等待权限审批时发送到本地服务器

假设服务器的接收地址为 `http://127.0.0.1:8787/events`，Python 已安装在 `C:/Python/python.exe`，转发脚本保存为 `D:/tools/notify.py`。以下路径仅为示例，配置时替换为实际位置。

1. 准备接收 HTTP POST 的服务器，接口要求见下文「本地接收服务器示例」。
2. 将仓库的 [Python 转发脚本](../examples/notifications/notify.py) 保存到自己的脚本目录。
3. 打开 TheOne 的「设置 → 通知」，按下表填写。
4. 点击「保存并发送测试事件」，检查服务器是否收到 JSON，以及设置页是否显示调用成功。
5. 开启「启用自动通知」并保存。之后检测到新的权限审批等待时，就会自动调用脚本。

| 设置项 | 填写示例 |
|---|---|
| 程序路径或命令名 | `C:/Python/python.exe` |
| 启动参数（JSON 数组） | `["D:/tools/notify.py", "http://127.0.0.1:8787/events"]` |
| 工作目录 | 留空 |
| 执行超时 | `10` 秒 |
| 通知事件 | 只勾选「等待权限审批」 |
| 测试事件 | 选择「等待权限审批」 |

测试时可以保持自动通知关闭。测试数据带 `test: true`，真实事件带 `test: false`，服务器可以据此区分。

如果更喜欢 Go，可使用 [Go 转发程序](../examples/notifications/notify.go)。在仓库根目录编译：

```powershell
New-Item -ItemType Directory -Force .codex-build | Out-Null
go build -o .codex-build/notify.exe examples/notifications/notify.go
```

然后把「程序」改为生成的 `notify.exe` 的完整路径，参数改为 `["http://127.0.0.1:8787/events"]`。编译后的 exe 不需要接收通知的电脑安装 Go。

## 配置与调用约定

- **程序**：可执行文件完整路径或 PATH 中的命令名，例如 `python.exe`。脚本通过解释器启动，Go 程序建议先编译成 exe。
- **启动参数**：JSON 字符串数组，一个元素对应一个参数。不经过 Shell 展开，不自动去掉空格；支持空字符串。路径里的反斜线按 JSON 写成 `\\`，也可用 `/`。
- **工作目录**：绝对路径，留空使用 TheOne exe 所在目录。它不会自动跟随当前项目。
- **执行超时**：1–120 秒，默认 10 秒，从程序开始执行时计算，不包含排队时间。
- **事件**：等待回答、等待权限审批、任务完成，可分别勾选。当前正在看的会话同样发送。

不同语言的配置方式如下：

| 处理程序 | 程序配置示例 | 启动参数示例 |
|---|---|---|
| Python | `python.exe` | `["D:/tools/notify.py"]` |
| Go 编译后的 exe | `D:/tools/notify.exe` | `[]` 或该程序需要的参数 |
| Node.js | `node.exe` | `["D:/tools/notify.js"]` |
| PowerShell | `powershell.exe` | `["-NoProfile", "-NonInteractive", "-File", "D:/tools/notify.ps1"]` |

程序输入框只填写可执行程序本身，不把程序和参数混成一行。数组元素内部无需再包一层命令行引号。例如含空格的脚本路径直接写 `["D:/my tools/notify.py"]`。参数中的 `$()`、`&`、`%NAME%` 不会被 TheOne 展开；若指定的程序自身是 Shell，其参数解释由该 Shell 决定。

每次调用启动一个新进程，标准输入写入一个 UTF-8 JSON 对象和换行，然后关闭输入。程序退出码 0 表示成功，非零表示失败；标准输出被丢弃，标准错误仅保留有大小上限的简短错误供设置页展示。

Windows 后台调用不弹命令行窗口。调用串行执行，普通事件最多 32 条等待；队列满丢弃新普通事件并累计计数。汇总状态单独保留最新一条，队列满也不会丢掉最后的清除状态；过期中间状态可以合并。普通事件之间保持 FIFO，汇总状态与普通事件不承诺全局先后顺序。失败不自动重试。修改配置会取消当前调用并清空等待队列；Windows Job Object 清理本次调用派生的进程，因此通知程序应处理完毕后退出，不应用来启动常驻服务。

「保存并发送测试事件」先保存配置，再通过同一个队列调用程序。测试不受自动通知开关和事件勾选限制，带 `test: true`，不改变真实会话的状态或去重记录。设置页显示执行状态、排队数量、丢弃数量和最近一次结果。结果只在内存中保留，重启后清空；TheOne 不将调用输入、输出或结果写入应用日志。用户程序自身的写文件行为由用户程序决定。

## 事件格式（版本 1）

```json
{
  "schema_version": 1,
  "event_id": "唯一事件标识",
  "event": "permission_requested",
  "occurred_at": "2026-10-22T07:30:00Z",
  "provider": "codex",
  "session_id": "会话标识",
  "runtime_token": "本次终端标识",
  "session_name": "会话名称",
  "project_path": "D:\\projects\\demo",
  "state": "blocked",
  "waiting_for": "approval",
  "turn_id": "任务轮次标识（来源提供时）",
  "test": false
}
```

事件名为 `input_requested`、`permission_requested`、`task_completed`、`attention_changed`。新汇总事件需要单独勾选，原有配置不会自动订阅它。可选字段为空时省略；时间为 UTC。事件 ID 可供接收服务去重，但应用不承诺网络发送恰好一次。

| 字段 | 类型 | 含义 |
|---|---|---|
| `schema_version` | 整数 | 当前协议版本，固定为 `1` |
| `event_id` | 字符串 | 本次事件的唯一标识 |
| `event` | 字符串 | 事件名称，见下表 |
| `occurred_at` | 字符串 | 检测到事件的 UTC 时间，RFC 3339 格式 |
| `provider` | 字符串 | Agent 来源，例如 `claude`、`codex` |
| `session_id` | 字符串 | 来源提供的会话标识，可能带来源前缀 |
| `runtime_token` | 字符串，可选 | 本次终端运行标识；来源提供时发送 |
| `session_name` | 字符串 | 来源提供的会话名称，可能为空 |
| `project_path` | 字符串 | 来源提供的项目目录，可能为空 |
| `state` | 字符串 | 来源提供的当前状态，例如 `blocked`、`idle`、`done` |
| `waiting_for` | 字符串，可选 | 等待原因，例如 `approval` 或 `userInput` |
| `turn_id` | 字符串，可选 | 来源提供的任务轮次标识 |
| `test` | 布尔值 | 是否为设置页发起的测试事件 |
| `attention` | 对象，可选 | `attention_changed` 的全局汇总，见下文 |
| `force` | 布尔值，可选 | 为 true 时要求重新同步当前状态，相同值也可能调用 |
| `reason` | 字符串，可选 | 汇总调用原因：`state_changed`、`configuration_changed`、`shutdown` 或 `test` |

| 事件名称 | 对应界面选项 | 典型等待原因 |
|---|---|---|
| `input_requested` | 等待回答 | `userInput` |
| `permission_requested` | 等待权限审批 | `approval` |
| `task_completed` | 任务完成 | 无，此时通常不发送 `waiting_for` |
| `attention_changed` | 待处理状态变化（含全部清除） | 全局汇总，不属于某个会话 |

处理程序应按 `event` 分流，避免依赖不同 Agent 可能各异的 `state` 值。额外字段应允许忽略，以便后续兼容扩展。

只发送状态、会话名称和项目路径等信息，不发送终端输出、询问正文、待审批命令或对话内容。当前状态来源没有完整的审批详情。

## 触发语义

前三类单会话事件中，每个来源的首次成功快照只建立基线，不补发已有等待或历史完成。相同会话持续等待同一种操作时只通知一次；明确恢复后再次等待，或者从权限审批变为询问，才产生新事件。未知等待原因不猜测为审批。

Codex 通过任务轮次 ID 对成功完成去重，失败和中断不算完成；Claude 依据明确的完成状态或执行中转为空闲判断。会话消失、关闭、查询失败不会被推断为成功。启用或修改通知配置不会重放此前的状态。

前三类通知依据后端观测到的状态变化，不是无损事件流。Claude 使用轮询，Codex 更新信号可能合并，因此持续时间很短且没有被观察到的中间状态可能不通知。前三类通知不依赖设置页打开、窗口焦点或任务栏角标。

### 待处理状态汇总：`attention_changed`

该事件适合外设灯、托盘状态或需要知道“全部处理完毕”的程序。它跟随界面已有的等待和未读状态，首次取得状态、状态改变、保存配置和窗口重新聚焦时同步。单会话字段为空，关键载荷如下：

```json
{
  "schema_version": 1,
  "event": "attention_changed",
  "attention": { "active": true, "waiting": true, "unread_count": 2 },
  "force": true,
  "reason": "state_changed",
  "test": false
}
```

- `waiting`：至少一个会话仍在等待输入或权限审批。查看会话不会代替回答或批准。
- `unread_count`：任务完成或普通会话结束后尚未查看的会话数，按会话身份去重；包含原生任务栏完成数字未计入的普通结束。
- `active = waiting || unread_count > 0`。只有两个来源全部清除才变为 false。
- 在窗口可见、有焦点且查看对应会话时清除该会话的未读；关闭会话也会清理。打开其他会话不会清除它。
- `force: true` 是重新核对状态的请求；程序必须幂等。外设脚本应先读设备，已经匹配就不写入。设备重新连接后可聚焦 TheOne 触发重新同步。
- 禁用、取消勾选或更换程序/参数/目录时，旧程序收到一次全 false 的清除通知，再由新配置同步当前状态。清除失败仍会保存新配置，并显示警告。
- 正常退出先停止当前调用，再用独立超时（最多 3 秒）向已订阅的程序尽力发送全 false 状态；不重试。强制杀进程、掉电或设备不可用时不能保证熄灯，下一次正常启动重新同步。

未读状态与现有界面一样只在内存中保留，不在重启后恢复历史未读。汇总事件依赖界面状态同步，前三类后端事件继续保持原有语义。此事件不直接表示一次新任务或新询问，不应把每次 force 同步当作新增提醒。

Magic Bay Light 可直接使用仓库的 Python 消费脚本，配置与设备限制见 [Magic Bay Light 通知接入](magic-bay-notifications.md)。

## 转发到本地服务器

服务器提供例如 `POST http://127.0.0.1:8787/events` 的接口，按 JSON 读取请求体，成功接收后返回 2xx。

仓库包含两个只依赖标准库的示例，均将原始事件 JSON 转发给指定服务器：

- [Python 示例](../examples/notifications/notify.py)：程序配置为 Python 完整路径，参数例如 `["D:/tools/notify.py", "http://127.0.0.1:8787/events"]`。
- [Go 示例](../examples/notifications/notify.go)：先运行 `go build -o .codex-build/notify.exe examples/notifications/notify.go`，程序配置为生成的 exe 完整路径，参数例如 `["http://127.0.0.1:8787/events"]`。

只勾选「等待权限审批」即可只转发该事件。认证请求头、消息格式转换或其他通知渠道都可在用户程序中自定义。服务器响应仅确认接收，本接口不接收或执行审批决定。

### 本地接收服务器示例

下面的服务器仅使用 Python 标准库。保存为 `server.py`，运行 `python server.py`，再在 TheOne 中发送测试事件。它会打印收到的 JSON，并返回 `202` 表示已接收。

```python
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/events":
            self.send_error(404)
            return

        try:
            length = int(self.headers.get("Content-Length", "0"))
            event = json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeError):
            self.send_error(400, "Invalid JSON")
            return

        # 在这里替换为自己的保存、转发或提醒逻辑。
        print(json.dumps(event), flush=True)
        self.send_response(202)
        self.end_headers()


ThreadingHTTPServer(("127.0.0.1", 8787), Handler).serve_forever()
```

### 自己编写处理程序

不发送 HTTP 也可以。处理程序只要遵循「读取标准输入 → 处理事件 → 以退出码报告结果」的约定即可。最小 Python 示例：

```python
import json
import sys

event = json.loads(sys.stdin.buffer.read().decode("utf-8"))

if event["event"] == "permission_requested":
    # 在这里调用自己的服务、通知工具或业务代码。
    pass
```

如果处理失败，程序应返回非零退出码，并把简短原因写到标准错误。标准输出不会显示在 TheOne 的结果区域。

## 排查问题

| 现象 | 检查方式 |
|---|---|
| 无法启动程序 | 使用可执行文件完整路径；Python 脚本应配置 Python 解释器，并将脚本路径放入参数数组 |
| 参数解析失败 | 参数必须是 JSON 字符串数组，例如 `["notify.py"]`；Windows 反斜线需要转义 |
| 提示连接失败 | 确认服务器正在运行，地址、端口和 `/events` 路径正确 |
| 提示执行超时 | 检查脚本是否已读完输入并退出；HTTP 请求自身也应设置合理超时 |
| 测试成功但没有自动调用 | 开启并保存自动通知，勾选对应事件；首次状态只建立基线，需要在之后产生新的状态变化 |
| 服务器收到测试数据 | 这是测试按钮的预期行为，可通过 `test` 字段识别 |
| 显示队列满丢弃 | 缩短脚本耗时或减少勾选事件；被丢弃的事件不会自动补发 |
| 没有收到问题或命令正文 | 当前接口仅提供状态和会话信息，不包含这些正文 |
| 重启后看不到上次结果 | 调用结果只保存在内存中；程序配置仍会保留 |

默认配置文件位于 `%LOCALAPPDATA%/ClaudeSessionManager/notification.json`。日常通过设置页修改即可；如果文件损坏，界面会报告读取失败，不会自动覆盖原文件。
