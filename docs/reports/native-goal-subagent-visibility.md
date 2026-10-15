# 原生目标与子智能体可见性

本轮实现目标暂停/恢复，并补齐同一事件路径遗漏的子智能体、计划与普通工具。用户明确授权部署及重启小内核后，已完成现场更新；本报告区分源代码、隔离原生协议、安装包及现场证据。

- Codex 目标通过 thread/goal/get → thread/goal/set → 独立 get 确认状态，保留目标内容和 tokenBudget。前端目标条提供暂停/恢复。
- Hermes 目标通过 session.control goal.pause/goal.resume，恢复执行原生返回的 kickoff。原生恢复会重置 turns_used；界面明确说明，保持原生回读，不伪造累计轮数。目标内容、完成条件和 max_turns 由原生控制保留。
- 子智能体发现包含 Codex 子来源，持久化父关系；侧栏与监控选择器不再按子来源或审查标题隐藏有效会话。
- Codex collabAgentToolCall/subAgentActivity 与 Hermes subagent 生命周期进入任务投影。spawn 工具完成不代表子智能体完成；状态来自子生命周期。
- 运行摘要包含子智能体、待办及普通工具。详情展示原生报告的模型、用量、工具次数、文件及公开结果，支持打开子会话。停止命令使用精确子会话作用域；不存在对应会话时禁用停止，避免误停父会话。
- Hermes todo.updated、Codex turn/plan/updated 以完整快照更新；移除步骤取消，旧 revision 不覆盖新状态，作用域包含 Agent 与会话。
- Codex 原生历史恢复缺失子元数据，优先最新快照，已有实时任务不被旧历史覆盖。聊天保留原生读取路径。

已验证：

- 前端全量 102 文件 / 439 测试通过；JSDOM 留有 Window.scrollTo 未实现提示，无测试失败。
- Session Daemon、App Server 全量 Go 测试通过；双内核 vet、contracts 测试、desktop-wails 测试通过。
- 新增历史回归通过：同页旧 running 与新 completed 以新状态恢复；后续历史读取保留实时 running。
- 前端父会话任务详情停止回归通过，实际 API mock 接收 session_id=child、target_id=child，未发父作用域。
- 真实 Codex 0.161.0 在临时 CODEX_HOME 中执行 Go adapter 暂停/恢复/清除，独立回读确认目标与预算。
- 真实 Hermes gateway 在临时 HERMES_HOME 中执行原生暂停/恢复/清除，独立回读确认状态、目标与 max_turns，恢复返回原生 kickoff。该检查未提交模型推理；Go adapter kickoff 提交由协议回归覆盖。

- 安装包 canonical build 完成，含 TypeScript 编译、Vite 生产构建、Wails 客户端及 12 个 Go 构件。安装器隔离全新安装、覆盖升级、构件逐字节校验、App 重启且 Daemon 进程身份不变、卸载保留隔离数据均通过（exit 0）。
- 最终产物 release/Astrorder-Setup-0.1.0.exe：126634588 bytes；SHA256 `7d989bae06bc6c7173b651c7373e2bb15f058abb52512fdf65896afb4a4cd0ba`，已独立计算并与 manifest 核对。release 内无 ZIP。

现场部署：

- 用户授权原文：“部署一下，可以重启小内核”。完整安装目录备份至 `C:/Users/luwei/AppData/Local/Astrorder/backups/goal-subagent-20261013-230909/installation`。
- 停止前通过权威 CLI 再次读取为 146 条登记。停止精确安装客户端、大内核、小内核后，执行已核验安装器覆盖升级，再启动双内核与客户端。部署脚本退出码 0。
- 部署完成时小内核 PID 5644、大内核 PID 66448，权威登记仍为 146 条。继续核验时进程已变为小内核 PID 20120、大内核 PID 20380；本次继续操作未发起重启，变化原因未调查。当前身份验证、监听归属与健康检查均通过，权威登记仍为 146 条，并报告当前 Hermes 会话正在运行。
- 最新核对：客户端和 12 个构件哈希与验收构建一致；HTTP 首页及主 JS 实际字节与构建一致。
- 实装 HTTP 临时会话创建、列表读回、删除后 404 通过；目标 status PUT 到达原生控制，对空目标明确返回 `native goal no longer exists`，该检查不代表真实目标暂停/恢复成功。
- 两次补验未通过：空白临时会话分叉返回 `daemon rejected session creation`；停止空闲临时会话返回 HTTP 502。这两项不计入成功证据，尝试记录保留。各次创建成功的临时会话均精确删除并读回 404。
- 未直接读取或修改生产数据库，未更改凭据或既有聊天，未 commit/push。未进行现场 UI 点击、已有目标的暂停/恢复或真实模型子智能体执行。

部署收据与日志位于 Hermes scratch/lifecycle-deployment.json、lifecycle-deploy-latest-verification.json、lifecycle-deploy-http.json；失败尝试记录为 lifecycle-deploy-http-first-attempt.json 和 lifecycle-deploy-http-second-attempt.json。构建与隔离日志为 lifecycle-installer-build.log、lifecycle-installer-isolation.log。
