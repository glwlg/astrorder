# LLM 网关模型同步

星序以网关模型目录作为统一来源，为 Codex、Grok 和已有的 Hermes 配置写入各自的推理地址。Hermes 的模型列表仍由 Hermes 自己向网关拉取，星序只同步地址和 Key，并校验动态目录。

## 配置

设置页的“LLM 网关与模型同步”包含：

- 网关类型：当前支持 OpenCodeX 和 Magpie。
- 管理地址：OpenCodeX 是提供 `/api/models`、用量和额度的地址。Magpie 是 `magpie web` 的站点根地址，不带 `/v1`，例如 `https://magpie.example.com`。
- 推理 Base URL：Agent 调用模型的 OpenAI 兼容地址。Magpie 仍是网关的 `/v1`。
- API Key：推理调用使用的 Key。保存后只返回掩码。
- 管理端访问参数：仅 Magpie。填写管理页链接中的 `k`。它和推理 API Key 不是同一个凭证，只用于读取 `/api/usage`、`/api/usage/requests` 和 `/api/usage/quotas`。保存后只返回掩码。
- 目标地址覆盖：为本机、特定 WSL 发行版或 SSH 主机指定不同的推理地址。例如网关所在的 Debian 主机可使用 `http://127.0.0.1:10100/v1`。

Magpie 的用量和额度不再读取本机 `~/.config/magpie/usage.jsonl`，也不再访问 `127.0.0.1:3425`。管理端会先用 `k` 换取访问 cookie，再返回统计。

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
Windows 已有的 %LOCALAPPDATA%/hermes/config.yaml
Linux、WSL、SSH 已有的 ~/.hermes/config.yaml
```

同步接口不能写入任意路径，也不会新建缺失的 Hermes 配置。Codex 的 OpenCodeX Key 写入 `OPENCODEX_API_AUTH_TOKEN`，Magpie Key 写入 `MAGPIE_API_KEY`。Linux 目标分别写入 `~/.config/environment.d/astrorder-opencodex.conf` 和 `astrorder-magpie.conf`，Windows 目标写入对应用户环境变量。只写非空 Key，两个 Key 互不覆盖。Grok 按其原生格式在每个模型配置中写入当前网关的 Key。

## Agent 映射

Codex 配置会保留顶层设置、功能开关和其他模型提供者，仅替换 `model_providers.opencodex`，并生成 `opencodex-catalog.json`。已有目录中同名模型的扩展字段会保留，网关事实字段会更新。

Grok 配置会保留 `cli`、`ui`、`marketplace`、`memory`、`plugins` 和全局 `models` 设置，重新生成 `model.*` 表。`ultra` 等 Grok 不支持的思考程度不会写入；xAI 模型缺少思考程度时使用 `high`、`medium`、`low`。

Hermes 只改已有 `config.yaml`：`providers.magpie` 对应该网关，OpenCodeX 优先写入已有 `ocx`，否则写入 `opencodex`。同步 `base_url`；有 Key 时才改 `api_key`。`model.provider` 命中时同步 `model.base_url`。文件不存在时预览状态为“未找到配置”。预览还会通过已连接的 Hermes 会话读取原生模型列表，并显示验证状态、模型数量和与网关目录匹配的数量。

## 任务与故障恢复

同步结果保存在星序数据库中，保留最近 20 次任务。大内核重启后，未完成任务显示为中断，不会自动重放。取消会阻止尚未开始的目标；已经进入原子写入的目标会先完成或回滚，避免留下半写配置。

同步不会调用 `pkill`，也不会结束 Codex Desktop、Hermes、Grok 或小内核进程。
