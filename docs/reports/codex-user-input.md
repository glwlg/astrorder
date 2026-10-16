# Codex 会话内询问

## 异步询问（2026-10-16）

原生 `request_user_input_async` 与下文同步请求不同：它生成带 `delivery:"async"` 和结构化 `questions` 的 `agentMessage`，没有同步 pending RPC ID。此前同步验收不能证明异步链路已实现。

App 历史及实时投影现在保留这两个字段；桌面和手机正文使用异步问题卡，支持选项、自行填写及多个问题。回答通过普通 `send` 命令进入现有输入链路，运行中由既有引导逻辑处理，空闲时进入下一轮；`target_id` 关联原生问题 item。只读会话禁止提交。卡片从发送回执和后续同会话原生用户输入恢复状态，乐观用户气泡不算答复确认。回执丢失或 unknown 时仅查询，不自动重发；明确失败后可修改并以新操作 ID 重试。

本次验证包括历史／实时投影、普通 HTTP 命令与幂等回执、真实桌面／手机组件、隔离浏览器点击／刷新／WebSocket，以及安装版 Codex 0.163.0 在干净 CODEX_HOME 下的异步协议执行。CLI 验证使用本地注入 Responses 模型输出，不能视为生产模型自主调用验收。`gpt-5-codex` fixture 返回 unsupported call；改为原会话实际模型标识 `codex/gpt-6.1-sol` 后，在现有 `default_mode_request_user_input` 启动参数下通过；没有修改生产模型配置。

已发布 App、桌面嵌入前端及浏览器／PWA 前端。前端全量 603 项通过；App 候选回归汇总 1089 项通过、12 项跳过，相关 vet 和构建通过。App 候选使用隔离副本，排除工作区尚未接入的 `internal/projection/grok_events.go` 及其 `grok_features_test.go`，原工作区文件保留；全量首次失败和补齐扩展／路由基线／Git Bash 环境后的三包重试结果均保留。音频生命周期测试在独立系统进程中通过。该结果不是当前混合工作区全量无条件通过。

安装版接口只读回读确认原会话 `01a13d6f-79b2-7f21-b764-dfd43499922e` 的问题 item `call_Z423ZrE3wtMgz85eA62wLG4T` 保留 async、1 个问题、3 个选项及后续原生用户输入。安装 origin 的桌面和手机隔离 API 点击／刷新／实时事件验收通过，未向生产 Agent 发送新输入。113 个安装静态文件与候选 manifest 一致，主脚本、首页和 PWA worker 的线上字节一致，App／桌面 EXE 哈希一致，健康检查正常。发布工作进程退出后服务仍运行；小内核 PID 14628、创建时间及文件哈希不变，114 个后代进程身份全部保留。

本次运行时代码无新增生产修改，异步修复无需激活小内核候选。构建、发布、安装回读及截图证据位于 Hermes scratch 的 `codex-async-input/`。没有重建安装包、提交或推送。

## 同步询问历史记录

本轮接入原生 `item/tool/requestUserInput`。小内核保留原生 JSON-RPC 请求，投影问题、选项和待答状态；桌面正文、桌面详情、手机正文及手机状态面板使用共享问题卡。支持文本、选项、自行填写和跳过；通知显示“Codex 等待你的回答”。普通聊天问句不会触发原生询问。

回答经独立 POST `/api/v1/sessions/{session_id}/user-input`、gRPC `RespondApprovalRequest.user_input` 返回原生请求，不创建普通聊天命令，不启动新 turn。App 与运行时校验 question ID，使用原始请求 ID；正式 gRPC 用例覆盖 `9007199254740993` 的数值精度、本地及 SSH Codex registry 类型。重复使用相同操作 ID 和相同回答读取已有回执；改变回答复用操作 ID 会被拒绝。已解决请求仍允许相同操作回执重试，以处理原生 resolution 先到而 HTTP 回执丢失的情况。

浏览器验收发现并补齐手机正文入口，避免询问误显示为“允许本次／拒绝”。手机正文按悬浮输入区实测高度预留底部空间，避免任务及等待提示增高后遮挡问题按钮。

验证结果：

- 前端全量：110 文件、553 测试通过；TypeScript 和生产 Vite 构建通过。
- App：store、projection、sessions 三包完整回归通过；相关 vet 及 App 可执行文件构建通过。
- 小内核：runtime/codex、grpcapi 两包完整回归通过；相关 vet 及可执行文件构建通过。
- contracts 全量通过。
- Chrome 隔离桌面／手机两项通过，覆盖填写、选项、答复、跳过、实时通知、resolution 移除、刷新恢复及无聊天 command。截图已检查。
- 本轮相关 tracked 文件的 diff whitespace 检查在 Windows CRLF 设置下通过；仓库全局默认检查会报告其他工作树 CRLF 行，不代表全局工作树干净。

验证使用临时存储、模拟原生协议、隔离 HTTP/WebSocket；没有读取生产聊天／数据库／秘密，没有运行真实 LLM 询问或真实 SSH 主机验收，没有安装客户端或重启服务。

小内核候选：`P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`

SHA-256：`c1dda7e57a8ad48df9fa5b3053cc9597ae5eeccdc650fcc9bf447d6bfc3de48c`

暂存后逐字节哈希一致；已安装 EXE 哈希不变；小内核及直属进程的 PID 和创建时间不变；`update-check` 返回 `update_available:true`、`updated:false`。候选未激活，由用户在托盘“小内核 → 更新小内核”手动激活。

App 构建：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/astrorder-app-user-input.exe`

前端构建：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/codex-user-input-dist`

构建与暂存回执：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/user-input-staging-receipt.json`

2026-10-15 12:18（UTC+08:00）已按用户授权发布安装版前端壳、浏览器／PWA 前端、大内核及控制程序。桌面测试在独立 CIM 系统进程中全量通过；Wails 嵌入前端与浏览器前端分别构建，前者无 PWA worker，后者包含 worker；App 与控制程序重新构建通过。

安装位置为 `P:/DevApp/Astrorder`。已重启前端壳（PID 16652）和大内核（PID 39504）；安装 EXE 哈希与新构建一致，全部 113 个安装静态文件及其线上 HTTP 字节与新构建一致。`/health` 正常，控制器核对大内核身份与端口所有权通过。发布工作进程退出后再次核验服务仍运行。

小内核保持原 PID 18488、创建时间、端口 56572 和运行文件哈希不变；发布前 5 个直属进程的 PID、创建时间和路径保持不变。候选文件哈希保持本报告记录值，`update-check` 仍识别待更新。原生询问的小内核处理需要用户通过托盘激活候选后才能完整生效，本次没有进行生产原生询问交互验收。

2026-10-15 补充：用户实际 Codex 会话仍收到电影类型的普通 Markdown 列表。没有读取该生产聊天或数据库，因此不将截图那轮归因为已捕获的原生事件。调查发现当前 Codex 0.162.1 普通 Default 模式默认关闭 `default_mode_request_user_input`；此前验证仅覆盖原生请求到达后的处理，遗漏工具可用性。

已修改 `session-daemon-go/internal/runtime/codex/runtime.go`，为默认及显式配置的 `app-server` 启动参数添加 `--enable default_mode_request_user_input`，同时覆盖本地与 SSH 共用路径，保留测试 helper 参数且不修改原配置切片。无需切换 Plan 模式或更改全局 Codex 配置。

隔离真实 CLI 对比：未开启时工具描述仅允许 Plan，调用返回 `request_user_input is unavailable in Default mode`，无原生问题；开启后描述允许 Default/Plan，产生 `item/tool/requestUserInput`，选择“科幻片”通过原请求返回同一 `function_call_output`。另有显式 `ASTRORDER_REAL_CODEX` 的实际 Go runtime 测试验证询问、显式答复、原工具回读和轮次完成。均为本地注入 Responses 模型输出，不是生产 LLM 自主选择工具验收。

新增启动参数回归先失败，修复后通过；相关 runtime/codex 与 grpcapi 完整回归 231 项通过，vet 通过，真实进程专项 5 个测试节点通过。真实测试初次因缺少内部 Decision 字段失败，修正调用后功能通过但 Windows TempDir 清理失败；改为 Hermes scratch 独立目录并有界清理重试后整个专项通过。

新候选 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new` 已构建暂存，SHA-256 `e7089a30fd507300dc42104aff2988cb898217168bfeced3c1e8bc76acdda25b`；安装运行文件哈希不变，小内核及 19 个直属进程身份不变，`update-check` 返回 `update_available:true`、`updated:false`。回执：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/user-input-default-staging-receipt.json`。未激活小内核、未重启前端或大内核、未提交或推送。开关作用于新启动的 Codex transport；旧 Markdown 选项没有原生待答请求，不会自动转换为问题卡。

回滚备份：`C:/Users/luwei/AppData/Local/Astrorder/backups/user-input-20261015-121808`

发布及最终核验回执：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/publish-user-input/deployment.json`、`final-verification.json`。本次直接发布安装文件，未重建安装包；未提交、未推送。
