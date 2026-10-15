# 无目标会话误报取消与常驻目标创建入口修复

日期：2026-10-14。分支：`master`。本轮为源码、构建与隔离验收，未部署、未重启大小内核或客户端，未提交或推送。之前的本机安装记录不能作为本次修复已生效的证据。

## 用户要求与结果

目标沿用 Agent 已有命令设置。PC 输入框不再常驻“设置目标”按钮；独立移动端在没有目标时也不显示目标摘要或目标管理菜单。收到非取消的原生目标后显示状态和已有的编辑、暂停、恢复、删除操作；清除后入口消失。

进入从未设置目标的会话是正常空状态，不应弹出“任务失败 / 目标已取消”。原生受阻目标与普通任务的失败通知仍保留。

## 已复现根因与修复

- `app-server-go/internal/projection/goal.go` 原先把每次 `goal:null` 都投影成 `*:goal:current` 的 cancelled 任务，包括从未有过目标的会话。现按 agent、session、source 查询精确目标任务：不存在或已经 cancelled 时不写任务、不发事件；已有非取消目标第一次收到空快照时仍生成取消记录，使客户端清除旧状态。再次设置目标可替换该记录。
- `frontend/src/domain/notifications.ts` 原先把目标取消任务与普通失败任务一并通知。现根据 `progress.type=goal` 和 `status=cancelled` 跳过失败通知，兼容旧服务产生或重放的空目标记录。`failed/blocked` 的目标及普通失败任务不受此过滤影响。
- `GoalManager.tsx` 原先只要 Agent 支持目标就显示创建按钮。现仅在存在非取消目标时渲染。`MobileWorkspace.tsx` 的摘要与菜单也采用相同条件。Agent 命令菜单及消息发送路径继续使用原接口。

## 验证

证据根目录：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/`，属于可能被清理的临时缓存。

- 前端基线 473 项通过；本轮最终全量 Vitest **481/481**，0 失败，success=true，退出码 0。证据 `goal-entry-baseline-suite.json`、`goal-entry-final-suite.json`。新增空目标控制、取消通知、事件流与命令入口测试先失败后通过；定向最终 87/87。Go 新增用例首次夹具缺少 updated_at，补齐后再次确认真实缺陷失败，再修复通过。
- `tsc -b`、网页 PWA `vite build` 均退出码 0；`frontend/dist/` 已更新。首次类型检查在并行工作的星图测试夹具上报告缺少字段，其他工作更新后重跑通过；本轮未编辑该文件。证据 `goal-entry-types.log`、`goal-entry-final-types.log`、`goal-entry-build.log`。
- 最终浏览器 **15/15**，0 失败，退出码 0。运行当前 dist 和独立 HTTP/WebSocket 夹具，无生产会话、数据库或凭据访问。桌面 1440×960、手机 390×844 验证无创建入口、空目标事件静默、普通失败仍通知、命令菜单发送 `/goal ...`、目标事件出现与清除；移动目标生命周期继续覆盖暂停、恢复、编辑与删除。证据 `goal-entry-final-browser/results.json`、`goal-entry-final-browser.log` 和 `empty-goal-desktop.png`、`empty-goal-mobile.png`。此前桌面场景因误用手机连接标签及夹具缺少侧栏用量接口失败，修正夹具后重跑全部通过。
- `go test ./internal/projection ./internal/sessions -count=1` 通过；空目标测试覆盖 Codex/Hermes、重复空快照、清除已存在目标、再次设置以及同 session ID 不同 Agent/source 的隔离。证据 `goal-entry-final-go.log`。
- App Server `go vet ./...`、`go build -trimpath ... ./cmd/astrorder-app` 通过。候选二进制 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/goal-entry-astrorder-app.exe`；证据 `goal-entry-app-vet.log`、`goal-entry-app-build.log`。
- App Server 全模块 `go test ./... -count=1` **未全部通过**：`cmd/astrorderctl` 和 `internal/lifecycle` 共 12 个测试失败，输出为安装夹具 Hermes stdio gateway unavailable，以及 Windows 子进程 Access is denied。projection、sessions 在此全量执行中也通过。未修改 Hermes 安装或进程独立保护来规避失败，未将全 Go 回归计为成功。证据 `goal-entry-app-tests.log`。
- 定向 oxlint 退出码 0，0 错误；保留 `MobileWorkspace.tsx` 的 11 条警告。gofmt 检查无差异，限定文件 `git diff --check` 通过。证据 `goal-entry-lint.log`。
- 独立 Codex 只读限定审查：最终 JSON 符合要求的五字段 schema，passed=true，无安全或逻辑问题；首次返回遗漏两个字段，不计有效审查通过，补充格式要求重试后验证。证据 `goal-entry-review-verdict.json`、`goal-entry-review-final.json`。

## 边界

浏览器夹具证明实际前端交互和协议形状事件处理，不代表真实 Agent 或真机完整执行验收。本轮未改 Session Daemon，也未读取用户聊天或生产数据库。Wails 嵌入资源、已安装应用和安装器本轮未更新；当前安装版本仍是此前部署版本。
