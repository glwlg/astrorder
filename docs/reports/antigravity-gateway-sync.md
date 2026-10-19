# Antigravity 网关下发

星序的“设置 → 模型与网关”增加本机 Antigravity (agy) 目标，沿用现有预览与下发任务。WSL/SSH 暂不展示 agy，接口也拒绝远程 agy 选择。

## 配置契约

原生 CLI 的网关配置入口是用户目录下 `.antigravity/settings.json`，与 `.gemini/antigravity-cli/settings.json` 分开。网关使用 `gateway.enabled`、`gateway.url`、`gateway.apiKey`、`gateway.wireProtocol="openai"`；目录使用 `models.list[].modelId/displayName`。模型 ID 保持网关原值，包含 `group/luozhi` 等斜杠路由。

渲染器保留其他用户设置及 gateway 的其他字段，替换网关模型目录。预览对 JSON 中的凭据、token、secret 和 headers 脱敏。损坏的已有 JSON 明确失败。小内核新增 ManagedFile 枚举及 JSON 校验，继承原子的文件替换、回滚和 expected SHA-256 并发保护。缺少该文件状态的旧小内核会返回明确的更新提示。

agy 单独下发不会写入 Codex/Hermes/Grok 的凭据环境文件。下发任务只有在重新读取目标文件并精确确认内容及 SHA-256 后才报告成功。CLI 在新建会话或切换模型重接时读取新配置，不自动终止已有用户会话。

## 原生证据

- 私有 HOME/USERPROFILE 和 `--gemini_dir` 下，`agy models` 读取了私有网关目录。
- gRPC Apply → 文件读回 → 原生 models → stream-json 的专项验收通过。
- 本地测试服务确认 CLI 请求 `/v1/chat/completions`、模型 `group/luozhi`、Bearer 认证与流式响应。
- 使用用户当前 Magpie 网关，在独立私有目录通过 `group/luozhi` 获得真实回复；原生 init 模型一致、result 为 SUCCESS、唯一回复标记匹配。测试配置在结束后删除，未改用户生产 agy 配置。

## 回归与交付

本次冻结候选 App 测试 1174 通过、10 跳过，小内核测试 652 通过、28 跳过，前端测试 664 通过、0 失败。两 Go 模块 vet、前端 lint/typecheck、协议测试及生成重复性检查、桌面与浏览器构建均通过，真实原生 typed 专项通过。

隔离浏览器完成 agy 勾选及预览、下发任务 success、再次预览零差异、原生模型列表与绑定确认、真实 CLI 向本地测试网关发请求、原生历史及聊天正文显示。另有当前真实 Magpie 网关请求成功证据。

App、桌面及 114 个浏览器静态文件已发布，安装文件哈希和 HTTP 资源原字节核对通过。安装版 agy 选项及旧小内核更新提示已在浏览器验证。运行中小内核及其 13 个直接子进程的 PID、创建时间与可执行路径保持一致。

小内核候选只暂存 `astrorder-sessiond.exe.new`，SHA-256 为 `12fa8e80c60900d9c66def9ca459b28c4287f012a6ef865a86dbe0f934af032b`，update-check 已识别。遵循根 AGENTS.md，由用户从托盘激活；生产小内核尚未切换，生产 agy 配置尚未下发。构建、浏览器和发布的精确回执保存在 Hermes scratch `agy-gateway/`，以对应 JSON 文件为准。
