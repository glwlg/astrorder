# Session Daemon 维护入口

Session Daemon 的唯一实现是 `session-daemon-go/`。Python 小内核已移除；删除前源码保存在 Git 检查点 `603835a`。

## 代码归属

| 职责 | 维护位置 |
| --- | --- |
| IPC 服务、Connector、重放与持久化操作回执 | `session-daemon-go/internal/protocol/` |
| WAL、SQLite、保留策略 | `session-daemon-go/internal/events/`、`internal/journal/` |
| Codex、Hermes、Grok、SSH、PTY 进程与会话托管 | `session-daemon-go/internal/runtime/` |
| 模型配置写入与重载 | `session-daemon-go/internal/modelconfig/` |
| App IPC 客户端与事件投影 | `backend/src/astrorder/daemon/bridge/` |
| App 控制代理、Desktop 输入格式化、终端转发 | `backend/src/astrorder/daemon/clients/` |

不得在 Python 中重新实现 Session Daemon，也不得添加自动回退启动路径。Python 大内核的其他适配器、Connector 和 SSH 文件操作不属于此次删除范围。

## 启动

在 `session-daemon-go/` 执行 `go build -o <绝对输出路径>/astrorder-sessiond.exe ./cmd/astrorder-sessiond`。Python 生命周期脚本仅负责启动配置好的 Go 程序：

- `ASTRORDER_SESSION_DAEMON_EXECUTABLE`：Go 二进制绝对路径。
- `ASTRORDER_SESSION_DAEMON_CONFIG`：Go runtime JSON 配置绝对路径，格式见 `internal/configuration/config.go`。
- `ASTRORDER_SESSION_DAEMON_DB`：独立小内核 SQLite 绝对路径。
- `ASTRORDER_SESSION_DAEMON_SECRET`：由原有私密环境加载流程提供。
- `ASTRORDER_SESSION_DAEMON_ENDPOINT`：App 连接的本机 WebSocket 地址。

`start_daemon.py` 只接受 `--port`。旧 `--enable-codex`、`--enable-hermes` 等 Python runtime 参数已删除；runtime 由 Go JSON 配置控制。App 的 `ASTRORDER_DAEMON_*_ENABLED` 设置仍用于启用相应控制客户端。

沿用 `AGENTS.md` 的权威状态和重启约束。App 更新不得顺带重启小内核。本次是源码清理，未更新安装目录或重启生产服务。

## 验证

- Go：`go test ./... -count=1 -timeout=120s`、`go vet ./...`。
- Python：在 backend 虚拟环境执行 `python -m pytest -q`。
- 设置 `ASTRORDER_GO_SESSIOND` 为新编译的测试二进制绝对路径，可启用隔离 Go/Python 集成测试；临时数据库和测试进程不连接生产小内核。
- `test_go_only_architecture.py` 防止旧服务、runtime 目录和 Python 启动回退重新引入。

旧 Python 服务/runtime 专属测试随实现移除，其职责由 Go `events`、`journal`、`protocol`、`runtime`、`modelconfig` 测试覆盖。App 投影、控制客户端、分页、丢失回执恢复等测试仍保留；跨语言测试覆盖真实 Go 重放、App 重建、未成功投影不确认消费、幂等重放、模型配置、PTY、断开和日志保留。

`docs/design/` 和 `docs/reports/` 中较早的迁移描述是历史证据，涉及 Python 小内核的路径或命令不再是维护入口。
