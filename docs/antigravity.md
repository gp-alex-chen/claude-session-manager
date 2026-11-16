# Antigravity CLI 接入

TheOne 将 Google Antigravity CLI 作为第三种 provider，保留官方 TUI，在 ConPTY 中运行并通过 xterm.js 显示。项目分组的 `+` 提供“新建 Antigravity 会话”，支持多终端、混合分屏、调整大小和关闭。

## 本机准备

在 PowerShell 按 Google 官方说明安装：

```powershell
irm https://antigravity.google/cli/install.ps1 | iex
```

重新打开终端，运行 `agy --version` 和 `agy`，自行完成首次配置、条款与数据选项及登录。已运行的终端可能仍保留旧 PATH；必要时仅在当前 PowerShell 中执行 `$env:Path += ";$env:LOCALAPPDATA\agy\bin"`。

TheOne 优先使用 PATH 中的 `agy`，缺失时检查 `%USERPROFILE%\.local\bin\agy.exe` 和官方默认 `%LOCALAPPDATA%\agy\bin\agy.exe`。不会扫描其他目录或修改全局 PATH。若 Store 版 PowerShell 无法看到默认 AppData 安装，可用官方安装脚本的 `--dir` 选项安装到用户 `.local\bin`，再重新打开终端。需要 CLI 支持 `--settings` 会话级设置、官方 `statusLine` 和 ACP v1 会话列表；真实状态与指定 ID 上下文恢复验证使用 1.5.9。

## 当前能力

| 能力 | 行为 |
| --- | --- |
| 新建终端 | 使用所选项目目录启动官方 CLI；登录与审批在终端内完成 |
| 标题和任务状态 | 官方回调更新状态与显式标题；回调标题为空时，按观察到的原生 ID 和工作目录从历史同步自动生成的标题 |
| 尚无回调 | 显示“终端已打开 · 任务状态未知”，不猜测执行状态 |
| 自定义状态行 | 若全局 settings.json 已启用自定义 command，保留该行为；该终端在 TheOne 中保持状态未知 |
| 原生历史 | 通过 ACP initialize＋session/list 只读列出 ID、工作目录、标题与时间；失败保留上次成功历史并报告错误 |
| 指定历史会话打开 | 重新读取完整官方目录，精确核对 ID 和目录，启动 `agy --conversation <ID>`；原生身份尚未确认时状态显示未知 |
| 重启恢复 | 正常退出或更新时核对仍打开且身份明确的原生会话，保存后下次启动自动打开；未确认或身份切换有歧义的终端不加入清单 |
| 额度选择 | 来源支持 Antigravity 和自动跟随会话；按实际返回的模型组同时展示圆环，同组 5 小时与周额度套成双层，缺组或缺层时自动收缩 |
| 重命名、成功通知和用量 | TheOne 菜单原生重命名、成功通知和 Token 用量暂不支持；终端内 `/rename` 会更新观察到的标题 |

`working`、`tool_use` 显示执行中；`idle` 显示待命；`tool_confirmation_pending` 优先显示审批等待；`error` 显示错误。初始化、登录和审阅等未验证状态保守显示未知。状态行只在状态变化时触发，安静无回调不会被当作掉线；空闲、缺失回调、错误和退出都不会触发成功完成通知。

原生自动标题可能晚于对话结束生成，并在后续历史刷新时显示；标题同步本身只更新侧栏和分屏，恢复引用在退出时另行核对。终端内 `/rename` 返回的非空标题优先于缓存历史。

额度来自仍打开的终端中最近采集的一份有效官方状态行回调，套餐和额度桶保持同源，不拼接多个终端或声称代表当前登录账户。弹窗中的“读取终端额度”只重读本地回调，不向终端发送命令；需要更新原生额度时，在 Antigravity 中运行 `/usage` 后再读取。采集时间超过十五分钟会标旧；关闭全部终端后显示暂无回调。原生自定义状态行未接入助手时也可能没有额度数据。

只将合法的 `remaining_fraction` 转换为百分比，保留真实 0；仅有 `remaining_amount`、停用或畸形的额度桶不换算百分比。已核对的 `gemini-5h`／`gemini-weekly` 属于 Gemini 组，`3p-5h`／`3p-weekly` 属于第三方组；每组短周期在外、长周期在内，彩色表示已使用，数字表示剩余。组和圈层按本次数据生成，第三方组消失不保留占位，同组多于两层也完整绘制并扩大空间。未知桶保留自身 ID 独立显示，未知周期不生成均匀使用参考线；重置倒计时以原始采集时间换算，不随读取向后移动。

## 实现与数据边界

每个终端在 `%USERPROFILE%\.cache\TheOne\antigravity-runtime` 下创建独立临时目录，通过 `--settings` 指定会话配置。这个目录位于 AppData 之外，供打包与非打包的 Windows 进程共享；偏好和会话管理状态仍保存在原有应用数据目录。全局 CLI 设置和项目文件不被改写。回调借助私有 CMD shim 调用当前 TheOne 的内部状态助手；助手在 GUI、更新器和状态加载之前退出，不会启动第二个窗口。

终端单独设置 PATH，使官方二进制和私有 shim 可用；同时设置 `NoDefaultCurrentDirectoryInExePath`，防止工作目录中的同名 shim 抢先执行。该设置仅影响此终端的后代进程；当前目录中的命令应显式使用相对路径。PowerShell 宿主使用 `-NoProfile`，避免用户启动配置额外运行初始化程序；后台 ACP 继续使用 `CREATE_NO_WINDOW`。

TheOne 为其管理的 TUI 和后台 ACP 子进程设置官方 `AGY_CLI_DISABLE_AUTO_UPDATE=true`，禁用托管进程的后台自动更新，以避免相关辅助进程可能触发外部终端，同时避免使用中的可执行文件发生替换。该覆盖只作用于这些进程及其后代，不修改全局环境或 CLI 配置；更新 CLI 时请在自己的普通终端执行 `agy update`。开关说明见 [Google 故障排查文档](https://antigravity.google/docs/cli/troubleshooting/#resolve-self-updater-locks-and-failures)。

助手只保存会话 ID、标题、状态、审批标记，以及经过验证的额度桶、套餐与本地采集时间；不保存邮箱、凭据、提示内容、transcript 或未验证的用量。写入使用临时文件和替换；Windows 的独立文件锁协调跨进程读写，读取争用时保留旧快照，后续读取最终提交。每个终端独立轮询，变更通过合并信号送回 App；关闭取消轮询并清理该终端的自建目录，有在途文件句柄时进行有界重试。

历史读取只调用官方 ACP initialize 和 session/list，不调用 load、resume 或 prompt，也不提供客户端文件或终端工具。后台 ACP 进程通过管道通信，在 Windows 中不分配控制台窗口。应用内按生命周期延迟创建并复用一个元数据 ACP 进程和连接，只初始化一次；每次需要新鲜目录时仍发出完整 `session/list`，分页查询串行，空闲时持续丢弃无需处理的通知。首次冷查询仍可能较慢，后续查询复用已经就绪的服务，不再逐次启动和退出进程。请求取消、超时、EOF 或协议错误会中止并回收该连接，当次报错，下一次查询才重建；排队调用取消不会关闭正在服务其他调用的连接。应用退出完成恢复清单核对后关闭服务，失败更新后重新允许延迟启动；旧请求和迟到清理不能发布到新的服务代次。关闭或重启也会使历史缓存失效。完整分页有时间、页数、记录数和消息大小上限；重复 cursor、冲突目录、缺少能力或中途失败不会被当作完整的空列表。Windows `/D:/…` 形式规范化后再比较；不信任服务端 cwd 筛选。时间字段缺失不影响有效 ID 和目录，界面显示“时间未知”。

普通历史查询的十秒缓存从查询结束后计时，包括失败结果；失败仍保留上次成功的历史。状态查询中每个 provider 只读取一次，避免重复打开 agy 的快照和锁文件。

本地布局和主题就绪后，主界面即可交互，历史和自动恢复在后台继续。来源查询并行进行，每个来源完成后单独更新列表并恢复自己的保存会话；失败仅影响对应来源，保留其他成功结果及可用旧列表。恢复只填空闲窗格，不覆盖等待期间用户打开的会话，也不抢回已经选择的焦点。

Antigravity 自动恢复通过 `BeginSessionRestore(provider)`、`RestoreSession(token, id)` 和 `EndSessionRestore(token)` 复用后端持有的一份完整新鲜历史。同一轮多个保存会话只查询一次目录；前端仅持有随机批次凭据，不能提交目录或历史作为启动依据。后端按已保存引用与目录交集授权，快照固定 adapter、CLI 路径、原生 ID 和工作目录，有效期两分钟。每个 ID 仅消费一次，关闭会撤销其在旧批次中的资格并取消在途准备；结束批次或生命周期换代只取消准备，不终止成功转移给终端 Manager 的 TUI。旧代际清理不能取消新批次。本轮允许恢复的集合来自后端在启动时捕获的原始清单，其他来源先启动并写盘不会削减它；用户关闭会同步记录撤销。正常退出时保留尚未成功启动、未被用户关闭的原始引用，供下次重新验证；成功注册 PTY 后立即移出这份待恢复集合，不能用旧引用绕过原生身份确认或退出查询失败。

手动打开前强制刷新目录，不用上次缓存授权启动；ID 缺失、目录不一致、目录不存在或查询失败均报错，不回退新建。等待预检期间关闭终端会取消启动；旧请求也不能恢复已关闭的 tab 或销毁同 ID 的新 tab。

耗时历史预检在全局启动锁外执行，独立终端启动、状态查询和应用关闭无需等待该预检。最终注册 PTY 前重新核对应用生命周期和取消状态；关闭与注册仍保持原子顺序，过期的准备结果会清理自建资源。同一会话取消后立即重开时，会先等待旧准备的资源清理，其他会话不受该等待影响。

原生 ID 是观察值，不是当前 TUI 身份的有序证明。每个 runtime 以首次有效 ID（历史打开时为指定 ID）为锚点；观察到不同 ID，或确认后变为空 ID，会永久撤销本 runtime 的恢复资格，迟到的旧回调不能重新启用。通过 TheOne 重新打开原生历史会建立新的 runtime，重新核对资格。

PTY 运行时保持 unbound。正常退出或更新前，停止接收新启动请求，在全局启动锁外进行有整体三十五秒限时的完整 ACP 查询（包含排队等待；获得查询权后，单次原生目录传输仍最多十五秒），精确核对最近确认 ID 与工作目录；最后再次检查 runtime 实例、身份和存活状态，再由终端 Manager 在同一生命周期锁内捕获、关闭并保存恢复清单。用户已关闭、自然退出、未确认、查询失败或代际过期的结果不能复活会话。重复退出不会用空结果覆盖已保存清单。下次启动仍按指定 ID 查询和打开；找不到历史或目录无效时提示失败，不回退新建。启动恢复期间暂停五秒自动刷新，并使用本次查询的局部历史匹配保存的 ID；手动切换来源筛选只影响列表显示，不能作废恢复数据。旧启动请求迟到不能清理或覆盖新一轮恢复，读取失败与确认缺失会分别提示。

该机制覆盖正常退出／更新后的恢复，不承诺进程崩溃、断电，以及任意原生 `/new`、`/resume` 切换后的最终当前身份。成功通知仍需要独立的官方完成语义，不能用 busy→idle 替代。

## 验证

普通 `go test ./...` 与 `cd frontend; npm test` 使用合成数据和临时目录，不依赖真实 agy、账户或用户历史。

构建 TheOne 后，可运行独立的 Windows 进程测试：

```powershell
$env:THEONE_ANTIGRAVITY_TEST_HOST = (Resolve-Path .codex-build/TheOne-antigravity-dev.exe).Path
go test -tags integration ./internal/provider -run 'TestAntigravity(ShellArguments|HostHelper)' -v
```

该测试只启动合成 CLI 和构建产物的状态助手，验证 cmd/pwsh 新建及恢复参数、特殊字符路径、工作目录同名 shim 隔离、跨进程锁以及白名单快照。它不调用真实模型，不能代替登录后 TUI 状态、真实审批与 GUI 人工验收。

真实测试需要显式指定专用会话的 `THEONE_AGY_LIVE_CLI`、`THEONE_AGY_LIVE_CWD`、`THEONE_AGY_LIVE_SESSION`，再单独运行 `TestAntigravityNativeCatalogIntegration`；不输出其他会话元数据。`TestAntigravityStorePowerShellSettingsVisibilityIntegration` 另通过显式设置的 Store PowerShell 和共享 runtime 根目录验证配置、shim 的跨进程可见性。

官方参考：[安装](https://antigravity.google/docs/getting-started?tab=cli)、[状态行接口](https://antigravity.google/docs/cli/statusline/)、[恢复参数](https://antigravity.google/docs/cli/commands/resume)、[Hooks](https://antigravity.google/docs/hooks/)。
