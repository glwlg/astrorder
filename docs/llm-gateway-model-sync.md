# LLM 网关模型同步

星序以网关模型目录作为统一来源，为 Codex 和 Grok 生成各自的本地配置。Hermes 继续直接读取网关模型列表，星序只校验其动态目录，不写入 Hermes 配置。

## 配置

设置页的“LLM 网关与模型同步”包含：

- 网关类型：当前支持 OpenCodeX。
- 管理地址：提供 `/api/models`、用量和额度接口的地址。
- 推理 Base URL：Agent 调用模型的 OpenAI 兼容地址。
- API Key：保存后只返回掩码。
- 目标地址覆盖：为本机、特定 WSL 发行版或 SSH 主机指定不同的推理地址。例如网关所在的 Debian 主机可使用 `http://127.0.0.1:10100/v1`。

旧版 `base_url` 会自动迁移为管理地址。旧的用量接口地址配置仍可迁移；推理地址使用默认值，保存后进入新结构。

## 同步流程

1. 大内核从 OpenCodeX `/api/models` 拉取启用模型，标准化并计算 SHA-256 目录指纹。
2. 大内核请求小内核读取目标上的固定配置文件，并生成差异预览。
3. 用户确认后，同步作为可取消的后台任务执行；不同目标并行，同一目标的文件作为一个原子写入单元。
4. 小内核校验 JSON/TOML，写入临时文件，备份旧文件，再执行原子替换。失败时恢复该目标已经替换的文件，每个文件最多保留 3 个备份。
5. 活跃会话不被停止。现有会话继续使用原配置，新会话读取新配置。

小内核只允许管理以下路径：

```text
~/.codex/config.toml
~/.codex/opencodex-catalog.json
~/.grok/config.toml
```

同步接口不能写入任意路径。Codex 的 API Key 写入当前用户环境；Linux 目标写入 `~/.config/environment.d/astrorder-opencodex.conf`，Windows 目标写入用户环境变量 `OPENCODEX_API_AUTH_TOKEN`。Grok 按其原生格式在每个模型配置中写入 Key。

## Agent 映射

Codex 配置会保留顶层设置、功能开关和其他模型提供者，仅替换 `model_providers.opencodex`，并生成 `opencodex-catalog.json`。已有目录中同名模型的扩展字段会保留，网关事实字段会更新。

Grok 配置会保留 `cli`、`ui`、`marketplace`、`memory`、`plugins` 和全局 `models` 设置，重新生成 `model.*` 表。`ultra` 等 Grok 不支持的思考程度不会写入；xAI 模型缺少思考程度时使用 `high`、`medium`、`low`。

Hermes 不参与写入。预览时，星序会通过已连接的 Hermes 会话读取原生模型列表，并显示验证状态、模型数量和与网关目录匹配的数量。

## 任务与故障恢复

同步结果保存在星序数据库中，保留最近 20 次任务。大内核重启后，未完成任务显示为中断，不会自动重放。取消会阻止尚未开始的目标；已经进入原子写入的目标会先完成或回滚，避免留下半写配置。

同步不会调用 `pkill`，也不会结束 Codex Desktop、Hermes、Grok 或小内核进程。

