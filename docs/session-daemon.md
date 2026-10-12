# Session Daemon 维护入口

Session Daemon 的唯一实现是 `session-daemon-go/`。Python 小内核已移除；删除前源码保存在 Git 检查点 `603835a`。

## 代码归属

| 职责 | 维护位置 |
| --- | --- |
| IPC 服务、Connector、重放与持久化操作回执 | `session-daemon-go/internal/protocol/` |
| WAL、SQLite、保留策略 | `session-daemon-go/internal/events/`、`internal/journal/` |
| Codex、Hermes、Grok、SSH、PTY 进程与会话托管 | `session-daemon-go/internal/runtime/` |
| 模型配置写入与重载 | `session-daemon-go/internal/modelconfig/` |
| App gRPC 客户端与事件投影 | `app-server-go/internal/daemonclient/`、`internal/application/` |
| App 会话控制与终端转发 | `app-server-go/internal/sessions/`、`internal/terminal/` |

大小内核均使用 Go；Hermes 原生插件由 Hermes 自己的运行时加载。

## 启动

在 `session-daemon-go/` 执行 `go build -o <绝对输出路径>/astrorder-sessiond.exe ./cmd/astrorder-sessiond`。服务生命周期由 `astrorderctl daemon start|stop|restart|status --root <安装根目录> --state-dir <状态目录>` 管理：

- `ASTRORDER_SESSION_DAEMON_EXECUTABLE`：Go 二进制绝对路径。
- `ASTRORDER_SESSION_DAEMON_CONFIG`：Go runtime JSON 配置绝对路径，格式见 `internal/configuration/config.go`。
- `ASTRORDER_SESSION_DAEMON_DB`：独立小内核 SQLite 绝对路径。
- `ASTRORDER_SESSION_DAEMON_SECRET`：由原有私密环境加载流程提供。
- `ASTRORDER_SESSION_DAEMON_GRPC_TARGET`：显式回环 IP 和非零端口，必填。
- `ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL`：gRPC 调用凭据，必填。

runtime 由 Go JSON 配置控制。

沿用 `AGENTS.md` 的权威状态和重启约束。App 更新不得顺带重启小内核。本次是源码清理，未更新安装目录或重启生产服务。

## 验证

- Go：`go test ./... -count=1 -timeout=120s`、`go vet ./...`。
- 在仓库根目录运行 Node 隔离 HTTP 验收：`node scripts/migration/isolated-http-verify.mjs <仓库绝对路径> <临时目录>`。
- 设置 `ASTRORDER_TEST_DAEMON_EXECUTABLE` 为新编译二进制绝对路径，在 `app-server-go/` 运行 `go test ./cmd/astrorder-app -run TestDaemonRestartWithRealIsolatedLifecycle -count=1`，验证私有状态目录中的真实重启。

`docs/design/` 和 `docs/reports/` 中较早的迁移描述是历史证据，涉及 Python 小内核的路径或命令不再是维护入口。
