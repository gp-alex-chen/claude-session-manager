# 使用自定义通知程序控制 Magic Bay Light

通过 TheOne「设置 → 通知 → 自定义通知程序」调用 [magic_bay_light.py](../examples/notifications/magic_bay_light.py)。只使用 Python 标准库，不依赖 PowerShell、第三方 Python 包、WCH DLL 或灯的固件更新器。

需要 TheOne `v1.0-wails` 或后续包含 `attention_changed` 功能的版本。此前的 `v0.100-wails` 只有单次事件，无法告诉脚本何时所有等待和未读都已清除。

## 灯光规则

以下任一条件存在时亮灯：

- 会话等待用户回答。
- 会话等待权限审批。
- 会话任务完成或普通会话结束后仍然未读。

多会话合并计算，处理其中一个不会提前熄灯。所有等待解除，且所有完成/结束消息都已查看或相关会话已关闭后熄灯。已经在前台查看的完成会话不会产生未读；正在查看的询问仍要回答或批准才算解除。

亮灯使用第 1 档；如果灯已经处于第 2/3 档且已启用，重复通知会保留当前亮度。脚本每次先读取设备，状态已符合就退出，避免反复写设备配置。真正切换时只发送一次普通灯光配置请求，并验证读回结果，不刷固件。

## 配置

1. 安装 Python 3.9 或更高版本，将脚本放在固定目录，例如 `D:/tools/magic_bay_light.py`。
2. 打开新版 TheOne 的「设置 → 通知」。
3. 开启自动通知，程序填写 Python 可执行文件的完整路径，例如 `C:/Python/python.exe`。
4. 启动参数填写 JSON 数组：

   ```json
   ["D:/tools/magic_bay_light.py"]
   ```

5. 工作目录可以留空，超时建议 10 秒。
6. **只勾选「待处理状态变化（含全部清除）」**，然后保存。

脚本直接读取标准输入，无需填 URL，也无需启动本地服务器。完整配置示例（路径按实际安装位置修改）：

```json
{
  "enabled": true,
  "program": "C:/Python/python.exe",
  "args": ["D:/tools/magic_bay_light.py"],
  "workingDir": "",
  "timeoutSeconds": 10,
  "events": ["attention_changed"]
}
```

TheOne 当前只配置一个通知程序。如果还要同时转发本地服务器，可在自己的包装脚本里调用灯脚本并转发事件。汇总载荷和队列语义见 [自定义通知程序文档](custom-notifications.md)。

## 手动验证

在终端使用自己的 Python 路径执行：

```powershell
python D:/tools/magic_bay_light.py --check
python D:/tools/magic_bay_light.py --on
python D:/tools/magic_bay_light.py --off
```

`--check` 只读设备，输出是否在场、亮度档和启用状态。`--on` 和 `--off` 明确执行相应操作。

点击「保存并发送测试事件」会做可见的灯光测试：原本关闭时短暂点亮约 1 秒，再恢复测试前的亮度和开关；原本已经亮着则保持当前亮度。四种测试事件都支持此行为，测试下拉框可以保持默认。测试时设备不在线会报告失败，不会显示成功却没有灯光反馈。请使用建议的 10 秒超时，让脚本有时间完成点亮和恢复。

真实自动通知仍只处理 `attention_changed`，其他三种单会话真实事件不操作灯。保存配置本身也会同步真实汇总状态，所以当时有待处理会话时，保存后灯会保持亮起。

## 支持范围与恢复

- Windows，唯一的 Lenovo Magic Bay Light HID collection。
- 硬件 `VID_17EF / PID_616C / REV_0101`，固件状态版本 `1.01` 和已验证的报告格式。其他版本会报错，不猜测兼容。
- 使用系统已有的 `HidUsb`，不替换驱动。未连接灯时成功退出，避免阻塞其他通知；重新接上后聚焦 TheOne，重新核对当前状态。
- 多个匹配设备、未知身份、版本或状态格式会返回失败且不写入；错误在设置页的最近结果中显示。
- 自动配置脚本与手动命令应由一个 TheOne 实例负责，避免多个程序对同一盏灯给出相反状态。
- 正常退出、禁用或更换通知程序会尽力清灯；设备拔掉、Python 出错、进程被强杀或断电时不能保证。可手动关灯，或下次启动后重新同步。
- 原有未读状态不跨应用重启持久化。灯反映当前 TheOne 实例里的待处理状态。

## 协议边界

脚本只使用 16 字节的 Feature Report 1，正常配置类型为 `0x01`，读取响应类型为 `0x02`。亮度字段限制在 0–3，开关字段使用 0/1；不写 Report 2，不发送升级入口或固件数据，不枚举未知命令。

状态响应和控制命令的字段并非完全同构：响应 byte 7/8 是固件版本，控制 byte 7 要使用响应 byte 11 的独立配置位。脚本保留该位以及现有自动亮度配置，避免开关灯时附带改变其他设置。

每次实际配置切换会由设备保存普通灯光设置，因此脚本的预读去重不可去掉，也不应无限循环发送同一个写命令。普通调用最多写一次，手动测试最多写两次（点亮、恢复），不自动重试。测试过程若被强杀或设备断开，恢复可能无法完成；重新聚焦 TheOne 会再次同步真实状态。脚本不保存文件、不访问网络、不创建日志。

无硬件单元测试：

```powershell
$env:PYTHONPYCACHEPREFIX = "$PWD/.codex-build/python-cache"
python -m unittest discover -s examples/notifications -p test_magic_bay_light.py
```
