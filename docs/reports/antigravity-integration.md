# Antigravity（agy）第四原生 Agent 接入

## 2026-10-18 原生控制与用量扩展（已交付，候选待激活）

- App、Wails 桌面和 151 个浏览器资源已发布；独立 readback 确认安装全量文件哈希、在线 index/service worker、App health 和 Agent 注册。运行小内核 PID 46876、创建时间、运行文件哈希及发布前记录的 14 个直属子进程保持不变。
- 小内核仅暂存 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`，SHA-256 `1bf11943bc3a3f89d47a6aa2888d44a076b7b74dae9caaea6b03a41ac0ea2a38`，`update-check` 确认识别更新，未激活。用户按 AGENTS.md 从托盘“小内核 → 更新小内核”激活后才加载新增原生能力。
- 最终隔离桌面/手机 UI 脚本全部通过，稳定截图已检查；证据为 `ui-results.json`、`ui-runner.json`、`desktop.png`、`mobile.png`、`deployment.json`、`installed-verification.json`，回滚副本在本轮 scratch `backup/`。无 commit/push。

- 本机 Windows owned agy 进程支持原生 `CancelCascadeInvocation`、`ForkConversation` 与 `DeleteCascadeTrajectory`。通过 PID 监听端口发现独立认证的 loopback 服务，不使用共享日志选端点；其他平台的服务发现明确不可用。
- 停止请求只表示原生接受，发送命令仍由 stream-json result 收敛。实际取消可能返回 SUCCESS，不强制把 completed 改为失败；真实 CLI 隔离夹具确认停止后同一 UUID 可继续聊天。
- 原生分叉按消息 step index 保留父历史和子历史，接通 gRPC、App、桌面消息分叉及手机普通分叉；不提供 agy worktree 分叉。修复前端 worktree/branch_name 与严格 App create_worktree/worktree_branch 字段不一致造成的 422。
- 删除仅允许空闲 owned CLI，会话目录记录消失才确认成功，原生可能保留孤立 SQLite 文件；不手动删除原生文件。未确认结果保留 pending/error、阻止发送；后续删除请求只核对目录，不重复提交原生删除。App 取得 confirmed receipt 后才删除本地会话。
- 用量保留原生累计快照，映射 thinking→reasoning、cache_read→cached；缺失不补零、不跨轮重复累加。累计输入不能当上下文占用，App 对 agy 明确 context_available=false。
- 自定义模型 supportsImages 根据 catalog 的 image 输入能力生成。真实 CLI + 本地网关证实原生 media 可送达 PNG，但对应 stream-json 轮次事件未接通，产品附件仍关闭。plain models 输出不能证明 effort 档位，移除虚构五档列表；原生 custom probe 返回 not adjustable 并拒绝 --effort high。
- 最新全量：App 1202 pass / 10 skip，小内核 666 pass / 32 skip，前端 680 pass / 0 fail；vet、lint、TypeScript 和客户端构建通过。保留继承生产环境的 App 首轮失败及统计页前端时序失败，隔离环境/完整复验通过。最新 agy+gRPC 定向 184 pass，真实 CLI 能力夹具与删除边界 2 pass。
- 验收使用真实安装 CLI、隔离 HOME/USERPROFILE、独立 App/daemon 和本地协议网关；不等同于外部真实模型或实体手机验收。桌面与 390×844 手机浏览器完整 UI 通过权限读回、工具拒绝后续聊、累计用量/未知上下文、原生停止后同会话续聊、消息/普通分叉，以及子会话删除和父历史保留。手机为浏览器模拟尺寸；发布记录以 scratch 最终结果为准。
- 附件、运行中引导、SSH、转交、可调思考档位与真实上下文占用尚未完整接通，不能宣称全能力适配完成。生产小内核未停止/重启/激活，新增能力候选必须由用户托盘激活。
- 本轮证据目录：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/agy-advanced/`。

## 2026-10-18 工具错误与权限选择修复（历史交付）

- 根因：stream-json 适配器只允许 `ACTIVE` / `DONE`，把原生合法的工具 `ERROR` 当协议损坏并关闭进程。真实 agy 1.2.4 在 headless 命令被拒绝时输出 `ERROR`，随后 `result.status=SUCCESS`；这会结束该轮，但会话仍可继续。
- 修复合法 step 状态解析，保留 `tool_info.error.message`。原生历史读取补上 `Step.error` field 31 中 CortexErrorDetails 的错误文本；不新增消息正文持久化。
- 桌面、手机和监控输入入口接通权限选择：按原生规则（需要批准的操作在 headless 中被拒绝，无法逐次确认）／完全访问（用户显式选择后使用 `--dangerously-skip-permissions`）。不提供未经原生确认的自动审批档位；实测 `--mode auto` 仍读回 request-review。
- 仅空闲会话可切换权限，重接保持 conversation UUID、模型及 effort，并校验 init.permission_mode。模型切换保留权限；App 在重新绑定后恢复已保存的显式选择并核对读回。没有选择的会话保持原生默认。
- 验证：回归测试先失败后通过；真实安装 agy + scratch HOME/USERPROFILE + 本地 fixture gateway 完成同一 UUID 的 manual → full_access → manual、原生命令拒绝／执行、后续消息及历史验证。完整隔离 App/daemon 浏览器实际点击桌面与 390×844 手机权限入口并读回确认；没有向生产会话或外部模型发送验收提示。
- 最终全量：App 1197 pass / 10 skip，小内核 661 pass / 31 skip，前端 678 pass / 0 fail；lint、TypeScript、Go vet、桌面测试和发布构建通过。小内核首次并行包测试短超时失败，独立 CIM 进程串行包测试最终通过。
- App、Wails 桌面及 114 个浏览器资源已发布并读回核验。小内核候选仅暂存到 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`，SHA-256 `17cc1970883f833519fa81e6e821bcd103dd7d4dd3fd30e15c223b5c91a846aa`，update-check 通过。运行小内核 PID 46876、创建时间、运行文件哈希和记录的 13 个直属子进程保持原样。
- 生产核心修复待用户通过托盘“小内核 → 更新小内核”手动激活；Agent 未停止、重启或更新运行小内核，未自动放宽生产会话权限，未重放失败消息。
- 证据与回滚副本：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/agy-permissions/` 下 native-adapter.log、ui-results.json、桌面/手机截图、*-summary.json、*-results.json、deployment.json、installed-verification.json 与 backup/。未 commit/push。

## 2026-10-17 真实对话修复与交付（历史记录）

以下更新替代下方初版关于“不支持模型切换／未做真实发送”的状态描述；下方初版验证仅保留作为历史记录。

- 修复成功写入输入后缺少 `accepted: true`，以及 connector 消息／命令 ID 被误作为会话 ID，导致发送错误提示和实时完成状态归属错误。
- 空闲会话支持同一 conversation UUID 重接指定模型，原生 init 严格确认 UUID 和模型；运行中或不确定状态拒绝切换，切换期间拒绝发送。思考强度独立禁用。
- 修复 runtime.owner 缺实际进程 PID 导致 App 投影退出。已存在的缺 PID agy 终态事件只投影状态，不伪造进程归属，不修改原始日志或生产数据库。
- CLI 来源旧会话续聊实测回复 `AGY_EXISTING_OK`。桌面来源历史位于 `.gemini/antigravity/conversations`，经 CLI 续聊实测 `trajectory not found`，因此提供历史只读。CLI 历史位于 `.gemini/antigravity-cli/conversations`。
- 原生 CLI 和 Go adapter 均完成同一会话三模型连续真实对话：`gemini-3.8-flash-low → claude-sonnet-4-6 → gemini-3.8-flash-medium`。
- 冻结候选、独立 App/daemon 数据目录、真实 agy 的浏览器验收通过。会话 `3f62cd6f-0bbd-4cfa-8961-7551b481a46c` 桌面三轮均确认发送成功、命令 completed、原生历史助手回复及实时界面回复；390×844 手机浏览器实际切回 Flash Low 后再发送一轮获得回复，模型列表可选、思考强度禁用。非实体手机验收；本轮未验证审批模式操作。
- 最终 App 全量 1169 通过、10 跳过；小内核 649 通过、27 跳过；vet/build 通过。前端 661 项通过，lint/TypeScript、桌面 tests/vet/build/check 和浏览器构建通过。
- App、Wails 桌面及 113 个浏览器文件已发布。独立读回验证安装哈希、健康状态、小内核身份和旧内核模型保护；当前小内核 PID 50092 及记录的 22 个子进程保持不变。
- 小内核候选只暂存到 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`，哈希和 update-check 通过，尚未激活。根 AGENTS.md 第 12 行要求用户在托盘“小内核 → 更新小内核”手动激活。核心对话回执和模型切换修复尚未在运行中的旧小内核生效；激活后刷新客户端使用。

| 本轮组件 | SHA-256 |
| --- | --- |
| App | `1b4b5048c65f9a401068879cb226bc233e6ebebf238834b081c155a5a7594eb9` |
| 桌面 | `2fe67bc7b5c32e608fbd455825b56177e2602f1ce6a2549e66b89c69eefcd6a2` |
| 小内核候选 | `f7cb88154af4132b79454057d410c97b228d332865e4e723a19a9f0247462e5f` |

本轮证据、失败尝试和回滚副本位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/agy-live/`，包括 `acceptance/browser.json`、`release/*-results.json`、`deployment.json`、`installed-verification.json`。首次端到端缺 PID 失败已保留并修复后复验。未 commit/push。

## 初版接入记录

已实现本机 `antigravity` runtime（默认 Agent ID `local-antigravity`），CLI 为 agy 1.2.4。沿用用户修正后的认证配置，没有恢复 magpie 配置或新增 GEMINI_API_KEY。

## 实现范围

- Go Session Daemon 托管双向 stream-json，保留原生 conversation ID，支持创建、恢复、本机多轮文本输入及流式输出；异常 EOF 不视为完成，输入写失败进入不确定状态。
- 从 Antigravity 原生 SQLite 只读查询模型、会话目录和分页历史；持久消息正文仍以原生存储为准。
- gRPC/protobuf、App API、发现与恢复、桌面及手机界面接线，原生名称保留为 Antigravity。
- 固定模型使用 `model_read_only`，与聊天 `read_only` 分离，避免模型不可变导致输入框被禁用。
- 当前不支持 SSH、附件、运行中引导/停止、分叉、会话内切换模型或思考强度、原生删除；不支持的入口隐藏或禁用，服务端明确拒绝。
- 目录查询目前排除 `killed=1`；尚未建立独立的原生归档标记验收，不宣称已验证所有归档语义。

## 验证

- Session Daemon 全量 641 通过、26 跳过；vet/build 通过。
- 前端最终 614 项全部通过，TypeScript 检查及桌面/浏览器构建通过。
- 真实原生只读验收：6 个模型、130 个会话、1,809 条历史项，分页无重复；没有向真实模型发送提示词。
- gRPC 模拟生命周期覆盖两轮文本及非文本输入拒绝；模拟验收不等于真实模型多轮验收。
- 已安装浏览器界面分别在桌面和手机尺寸验证：本机四个 Agent 卡片，Antigravity 唯一；两台 SSH 节点均明确显示暂不支持且禁用接入。当前旧小内核未注册新 runtime，本机卡片显示未安装/不可接入，尚不能在生产创建 Antigravity 会话。
- App 最终全量复验：1121 通过、10 跳过、0 失败。首轮连接初始化超时、CIM 独立环境中的音频生命周期及 bash 查找失败均保留在 scratch；逐项复跑通过后，在工具环境且明确设置 scratch 临时目录的完整复验通过。

## 已安装与待激活

App、桌面客户端及浏览器资源已发布。安装级校验确认 113 个浏览器文件与构建相符，在线 index/主脚本/service worker 字节匹配，HTTP health 正常，桌面 `--check` 支持内核管理；10 个既有 Agent 归属保留。

小内核候选已暂存到 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`，哈希读回匹配且 `update-check` 识别更新，未激活。安装 runtime 配置先备份，再只追加 Antigravity；读回确认原有条目及其他字段保持一致。

| 组件 | SHA-256 |
| --- | --- |
| App | `91f2e3fe324ddfa37216fc2357307d7a7f2f958e486338df5d6c6b28a034198e` |
| 桌面客户端 | `6bad7b0108053c7f79cace7b860bc9af6afb3760d9b7afde069ee770588c1419` |
| 小内核候选 | `b26ca28ffb3ce8d905849fdc3937a844dbd4ccd18d3145da9d93d368fc122739` |

发布前后小内核 PID 均为 37312，创建时间和运行文件哈希保持一致；记录的 58 个子进程全部保留。没有停止、重启或更新小内核。

依据 AGENTS.md 第 12 行，候选由用户在系统托盘“小内核 → 更新小内核”手动激活。当前没有托管会话现场无缝移交能力，保持候选待激活。生产接入、创建、真实模型多轮及原生历史联动尚待激活后验收。

完整证据和回滚副本位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/agy-integration/`。未 commit/push。
