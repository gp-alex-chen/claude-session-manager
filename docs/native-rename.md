# 原生会话重命名

`RenameProvider.Rename(ctx, rawID, name)` 是统一可选能力，注册中心通过
`hasRename` 暴露给前端。`App.RenameSession` 解码 provider 身份，串行执行
改名并校验名称：去掉首尾空白、非空、最多 120 个字符、不含控制字符。
改名成功后更新历史缓存，原生名称不再被历史本地 alias 覆盖。
归档和恢复显示仍使用本应用 `hidden`，永久删除和原生归档尚未接入。

## Claude

使用官方 Claude Agent SDK **0.3.293** 的离线
[`renameSession`](https://code.claude.com/docs/en/agent-sdk/sessions)，按会话
UUID 和实际项目目录定位会话。该函数只追加 `custom-title`，不创建 query、
不发送模型请求。历史读取保留最后一条标题，支持 JSON 转义和超过 5000 行
之后追加的标题。成功后立即失效 catalog 的短期快照缓存。

helper 源码是 `frontend/src/native/claude-rename.js`，通过 esbuild 的 Node
bundle 生成 `internal/provider/claude_rename_helper.cjs`，再由 Go embed 放入
exe。用户无需安装 SDK；运行 Claude 改名需要 PATH 中的 **Node.js 18+**。
缺少 Node 时返回明确错误。程序通过 `node - <JSON 参数>` 运行，脚本经 stdin
传递，不生成落地脚本、不使用 Shell。参数不拼入脚本，支持引号、中文和路径。
15 秒超时或取消会结束由本应用创建的 Node 进程。

`npm run build` 会重新生成 helper；只改 helper 时可执行
`npm run build:claude-helper`。Go 单独构建使用仓库中的已生成 bundle。

SDK 的版权声明如下，SDK 使用适用 Anthropic 的法律条款：

> © Anthropic PBC. All rights reserved. Use is subject to the Legal Agreements outlined here: https://code.claude.com/docs/en/legal-and-compliance.

不得将此 SDK 声明为本项目许可证下的原创代码。
SDK 及 bundle 中依赖的原始版权/许可证声明保存在
`docs/claude-rename-third-party-notices.txt`，同时包含在内嵌 helper 中。

## Codex

使用官方 app-server 的
[`thread/name/set`](https://learn.chatgpt.com/docs/app-server)；短期连接完成
initialize/initialized 握手后，直接修改持久会话的名称。不 resume、start
或发送 turn，不修改会话正文。成功后失效 10 秒历史缓存。原生名称由
`thread/list` 读取，支持在重启应用和 CLI 后保持名称。

新建 Codex 临时终端的持久身份策略保持原有范围：历史列表中的已保存会话
可以改名，临时行仍不自动恢复。

## 验证

普通测试使用 fake RPC、fake DOM 和临时文件。Node 测试用隔离
`CLAUDE_CONFIG_DIR` 执行打包的离线 helper，验证只追加标题、原始模式与
非零成本记录按字节保留、Unicode/转义和独立进程再次改名。

```powershell
cd frontend
npm test
npm run build
cd ..
go test ./...
go vet ./...
go test -tags integration ./internal/provider -run TestCodexNativeRename -v
go test -tags integration ./internal/app -run TestClaudeNativeRename -v
```

Codex 集成测试限定 CLI 0.161.0，使用合成 rollout 和隔离 CODEX_HOME，检查
新服务器读取新名称且正文未变。Claude 集成测试检查完整的
`rename → shutdown → persist → startup → restore`，使用隔离项目、fake PTY
和嵌入式 helper，不使用真实用户会话、凭据或模型服务。
