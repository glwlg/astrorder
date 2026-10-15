# 原生目标状态缺失排查与后端修复

## 结论和范围

用户反馈所有 Agent 的目标状态均未展示。已确认 Codex、Hermes 后端链路存在遗漏，同时共享前端只识别 `codex:goal:current`。后端修复后，按用户后续要求已补齐 Wails 共享前端的通用目标状态条。

此前展示修复已于 2026-10-13 获用户授权安装到本机，用户已确认启动正常且目标可见。以下为当时的开发与安装记录；最新无目标入口及取消误报修复的状态、证据见 [无目标会话修复验收](native-goal-empty-state.md)，本次后续修复尚未部署。

## 已确认的问题

| 层 | Codex | Hermes | Grok |
| --- | --- | --- | --- |
| 原生协议证据 | 本机生成的 app-server schema 定义 `thread/goal/updated`、`thread/goal/cleared`、`thread/goal/get` | 本机 gateway 源码定义 `session.control.update`、`session.control.read`，目标位于 `control.goal` | 当前适配器使用 ACP，未取得可核实的原生 goal 查询或事件契约 |
| 小内核 | 已转发通知，但恢复会话时未查询当前目标 | 未转发 `session.control.update`，恢复时未读取控制快照 | 未改动；不能据源码搜索或 CLI help 断言原生不支持目标 |
| 大内核 | 未将目标通知投影为任务 | 未将控制快照中的目标投影为任务 | 未添加未经核实的事件映射 |
| 前端 | `ChatComposer.tsx` 只查固定 `codex:goal:current` | 同一条件会忽略 Hermes 目标 | 同一条件也不能展示其他来源的目标 |

## 后端改动

- Codex 更新和清除事件写入 `codex:goal:current`，并产生 `task.upsert`。
- Hermes 小内核转发 `session.control.update`；大内核从 `payload.control.goal` 写入 `hermes:goal:current`，并产生 `task.upsert`。
- 使用已有任务存储、任务 HTTP API 和事件链路；目标 `kind=todo`，`progress.type=goal`、`progress.source` 指明来源，`progress.blocking=false`。目标状态不改变会话当前命令的运行或审批状态。
- 原生字段保存在 `progress`，包括 Hermes 的 `turns_used/max_turns`、Codex 的 `tokensUsed/tokenBudget/timeUsedSeconds`。`progress.native_status` 保留原生状态。
- `active/waiting → running`；`paused/usageLimited/budgetLimited → pending`；`complete/done → completed`；`blocked → failed`；明确清除或查询返回 `goal:null → cancelled`。
- Codex 恢复线程、读取最新历史页时调用 `thread/goal/get`。Hermes 取得恢复句柄及读取最新历史页时调用 `session.control.read`；已有本地历史读取分支仅在已有句柄时查询，不为目标读取强行打开会话。
- 补充读取最多等待两秒；不支持、失败或缺字段时不伪造清除事件，不阻断会话恢复。目标仍以原生 API/事件为准，没有增加目标数据库读取兜底。

## 隔离验证

Go 工具链：`go1.27.2`。

- 修复前，Codex 生命周期测试因没有 `task.upsert` 失败；Hermes 转发测试因没有事件失败，Hermes 投影生命周期测试也因没有目标任务失败。
- App：`go test ./internal/projection -count=1 -timeout=60s`，58 项通过；`go vet ./internal/projection` 通过。
- Daemon：`go test ./internal/runtime/hermes ./internal/runtime/codex ./internal/runtime/grok -count=1 -timeout=60s`，156 项通过；三个包 `go vet` 通过。
- 大小内核分别执行 `go build ./cmd/...`，通过。
- 回归覆盖状态转换、原生进度保存、同原生 session ID 的 Agent 隔离、清除、原生命令身份保留、事件 envelope 分派、重复投影不新增任务、恢复查询、旧原生版本不支持/查询缺字段不产生清除、Codex 外部 thread 结果不投影。
- 恢复协议测试使用隔离合成 RPC/SSH transport，并非生产原生会话端到端验收。Grok 包回归通过不证明其 goal 功能已实现。

## 前端（2026-10-13 已完成）

- 新增 `frontend/src/features/chat/goalStatus.ts`：按 `progress.type === "goal"` 识别任意来源的目标，跳过 `cancelled`（已清除），多个目标时取最新 `updated_at`。
- 状态文案用 `native_status` 区分：进行中、等待中、已暂停、用量/预算受限、受阻、已完成。Hermes 显示 `第 N/M 轮`（完成时为 `共 N 轮`）；Codex 显示 `tokensUsed/tokenBudget`；不存在的字段不显示。
- `ChatComposer.tsx` 改用通用选择器；`SessionRuntimeBar.tsx` 不再把目标计入待办数。
- 测试：`goalStatus.test.ts`、`ChatComposer.test.tsx`、`SessionRuntimeBar.test.tsx`；前端全量 101 个文件、430 项通过（包含后续附件回归）。
- 待做：刷新、事件重连、暂停恢复的真实界面验收，需要先部署后端修复。
- Grok 未变：先取得当前安装版本的真实 goal 契约，再实现映射。

## 目标管理（2026-10-13）

- 目标条增加修改、删除，完成目标也可操作；修改弹窗预填内容，删除使用 portal 确认，失败保留原目标并显示错误。小屏允许目标条换行。
- 新增 `PUT/DELETE /api/v1/sessions/{session_id}/goal?agent_id=...`，通过类型化 gRPC `SetGoal/ClearGoal` 调用原生适配器。按 Agent、连接及原生 session ID 校验归属，沿用操作持久化和重复请求保护。
- Codex 使用 `thread/goal/set`、`thread/goal/clear`，修改保留原生状态及 tokenBudget；独立 `thread/goal/get` 确认后返回并投影。
- Hermes 清除调用实际注册的 `session.control`（action 为 `goal.clear`）；修改使用 `command.dispatch` 的 `goal -- <text>`，按原生行为重置目标和轮次，并提交返回的 kickoff。界面明确提示完成条件、子目标、门禁也会重置；活动执行期间禁用修改，删除仍可用。删除不取消正在执行的工具。
- 操作均独立回读原生目标，缺字段、未清除或错误结果不报告成功。HTTP 回读先更新当前界面，WAL 通知维持持久化及重连后的投影。
- 前端全量 102 文件、436 项通过；TypeScript 构建通过；lint exit 0，已有 warnings 保留。大小内核全量测试及 vet、contracts 测试及 vet、Buf lint/生成、desktop test/vet 均在构建中通过。
- 已在独立 HOME/工作区运行实际 Codex app-server 和 Hermes gateway，验证原生 set/read/clear；Codex 状态和预算保持。未调用模型推理。首次真实 Hermes 验收纠正了不存在的 `session.control.act` 方法名。
- 实际 Edge 浏览器运行正式 `GoalManager` 组件，修改、取消删除、确认删除通过；此处 HTTP 使用隔离 fixture，不等于生产 Wails/真实 Agent 全链路验收。
- 新包 `release/Astrorder-Setup-0.1.0.exe`，SHA256 `7d0a84a2a819729b22341e55642bd7062ec5024438ac6f763fe9031d0852d48f`；隔离新装、覆盖升级、12 构件哈希、App 重启保留 Daemon、卸载保留状态通过。未生成 ZIP。
- 当前生产未应用目标管理。小内核状态接口未返回活动会话明细，不能以空字段认定可安全重启；本次升级需按 AGENTS.md 的活动/未知状态规则取得明确同意。上一次重启授权已用于展示版本的安装。
- Grok 仍缺可核实的原生 goal 管理契约，本轮不提供伪造管理入口。
