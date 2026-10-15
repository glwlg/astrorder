# 原生历史作为唯一持久消息来源

本次完成 App Server 消息持久化链路移除，已按用户授权更新安装版 Wails、App Server、控制器和浏览器静态资源。未提交或推送；生产小内核保持运行，新候选仅暂存。

## 行为

- Codex、Hermes、Grok 消息正文、流式片段、工具消息及原始消息通知通过实时事件发送，不进入 App SQLite 消息表或事件表。
- `message.delta` 在前端内存累积；`message.upsert` 更新完整快照。瞬时事件 `cursor=0` 不改变持久状态游标，前端按事件身份去重。
- WebSocket 重连和唤醒刷新原生历史及控制状态。原生历史读取失败明确返回错误；Codex 仍可通过原生只读查询读取未托管线程，不回退至 App 消息库。
- 旧事件表中的消息事件重放为不含正文的 `event.cursor`，保留游标连续性及旧数据库行。
- 工具和子代理任务仅持久化状态、身份等元数据，完整参数/输出/结果经 `task.live` 发送。
- 群聊 Agent 回复仅保存原生会话和消息 ID 引用，读取时向原生历史取正文，必要时翻页。旧群聊 Agent 正文副本不再作为历史来源；无原生引用的旧 Agent 回复不会返回。群聊用户输入及系统通知继续用于群聊编排。
- 命令记录保留提交输入、回执、幂等与引导投递状态。标题和 presence 使用命令记录/会话元数据/原生数据，不再依赖消息表。
- 消息表 schema 和现有数据保留。旧消息读写 helper 已移至 `internal/store/legacy_messages_test.go`，不进入正式二进制。

## 验证

工具链：Go 1.27.2；前端使用仓库锁定 Node v26.11.1。

- `go test -json ./internal/projection ./internal/sessions ./internal/agentapi ./internal/store ./internal/application ./internal/botgroups ./internal/httpapi -count=1`：7 包、332 个测试及子测试通过，0 失败。
- 真实 loopback WebSocket 回归：实时消息到达、不写事件表、重连不重放正文、旧事件仅推进游标；两个回归重复运行 10 次通过。
- 三种 Agent SQLite 边界回归：消息/工具输出实时到达，消息表与事件/任务输出列无正文副本。
- 群聊回归：仅保存引用、原生正文读取、跨页恢复、缺失消息/原生错误明确失败。
- 前端全量：108 文件、542 项通过；`tsc -b` 通过；Vite production build 通过。
- 定向 oxlint：无错误，`useEventStream.ts` 有两条现有 React ref/dependency warning。
- `go vet ./...`、App Server `go build -trimpath` 及 `git -c core.whitespace=cr-at-eol diff --check` 通过。

App Server 构建产物：

`C:/Users/luwei/AppData/Local/hermes/cache/scratch/astrorder-native-only-app.exe`

SHA256：`087990d58773535cf48483a918f02b18d332fff56fbea37127aace1e30500c9e`

前端产物：`P:/workspace/glwlg/ai/astrorder/frontend/dist`。

## 验证限制

此前 App Server 全量失败日志保留在 scratch 的 `native-only-app-full.log`。本轮复现确认，Agent 工具的 Windows Job 拒绝 `CREATE_BREAKAWAY_FROM_JOB`，同时影响生命周期子进程和 Hermes runtime 探测；Hermes gateway 实际可用。通过 CIM 启动 Job 外的隔离测试进程，保持独立进程标志和原有断言，四模块全量均通过：App Server 1003、小内核 584、Wails 21、contracts 19 个测试及子测试，四模块 `go vet ./...` 通过。对应日志为 `native-rollout-<module>.jsonl`。

真实 Hermes gateway 的独立会话隔离、创建/历史/重命名/删除、持久会话进程恢复三个 opt-in 测试已实际运行并通过。它们使用新建隔离 home，不包含模型推理。前端 542 项、类型检查、浏览器及 Wails 构建、lint 均通过；lint 保留已有 warning。

## 安装版部署

- 安装目录：`P:/DevApp/Astrorder`。Wails EXE、大内核、控制器均已替换并核对 SHA256；113 个浏览器静态文件同时核对磁盘和实际 HTTP 字节。
- 大内核运行 PID 8088，健康、进程身份和监听所有权通过；Wails 安装路径 `--check` 和启动身份通过。
- 小内核 PID 29524、创建时间及部署前记录的 28 个直属子进程保持不变。新小内核构建暂存到 `server/bin/astrorder-sessiond.exe.new`，哈希一致，`update-check` 返回有更新且未应用；由用户通过托盘激活。
- 安装版桌面/移动端浏览器隔离显示回归通过：cursor=0 实时显示、delta 累积、重复事件去重、临时输入退出、重连刷新原生历史；无页面错误。HTTP/WS 使用浏览器内 fixture，未读取生产聊天或发送生产 prompt。
- 第一次切换因 PowerShell 未等待 GUI EXE 结束而误读退出码，触发回滚并恢复健康。发布脚本改为等待真实进程退出并读回检查 JSON，第二次切换成功。
- 回滚备份：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/native-only-deploy-backup-20261014-235145`。
- 部署回执：scratch 的 `native-only-deployment.json`；浏览器验收：`native-only-installed-browser-report.json`。
- 安装器未重打。真实模型推理和用户实际会话互动未纳入本轮验证。Session Daemon WAL 与原生 Agent 自身历史未改动；这里移除的是 App 层的持久消息副本。
