# Astrorder Session Daemon Go 重构验收与能力清单 (Acceptance Report)

本文档记录生产切换和历史隔离验证，**切换不等于全量功能验收通过**。

## 生产切换记录（2026-10-08）

用户明确授权退出星序后切换，并接受后续在 Go 中修复。Go 已接管生产 30009 端口，PID 24388；桌面启动及既有开机任务均通过持久配置选择 Go。大内核重启后 PID 从 68552 变为 41956，Go PID 与 epoch 保持不变。

Windows Go 全套测试、vet、构建通过；匹配程序的 Python 启动/bridge 专项 25 项通过。生产配置先经过隔离启动、认证状态、实际进程优雅退出验证。切换时未结束命令为 0，Go 存储健康且 pending operations 为 0。备份目录为 `%LOCALAPPDATA%/Astrorder/backups/go-activation-20261008-145254`；详细程序路径、哈希和证据见 `session-daemon-go/VALIDATION.md`。

实际 App 状态：本机、Debian、WSL 的 Codex/Grok 均 connected；本机 Hermes offline，两个 SSH Hermes error。Hermes 的控制连接目前被 Go 作为普通会话 resume 处理，另有远程启动参数及 runtime Query 接入待修。**保留 Go 生产运行，不宣称 9 个连接全绿、推理已验证或启动性能已达标。** 以下 retention 结论为历史版本证据；其中“生产切换暂缓”已被本次明确授权与实际切换取代，8 小时长稳及生产回滚未执行。

## 当前验收结论（retention 候选版）

| 验收层 | 已核实结果 | 边界 |
|---|---|---|
| Windows Go | 全套测试及 vet 通过；已构建 retention 候选版 | 后续参数校验修复另有全套/vet通过，尚需匹配构建 |
| Debian Go | 全套 race 与 vet 退出码 0 | `debian-retention-final.log`，对应 retention 源码 |
| Python App | 557 passed、3 skipped、2 warnings | `backend-retention-suite.log`；跳过项不算验收 |
| Chromium → App → Go → SSH Codex | 真实执行、App 停机继续工作、同页恢复、审批、分叉历史/标题、双侧删除读回均通过 | `native-browser-retention.log`；产物 `astrorder-browser-native-mHJwv4` |
| SSH Codex/Hermes 生命周期 | 严格删除断言通过，包耗时 6.219s | Codex 删除后同 ID resume 拒绝；Hermes 临时 HOME 中删除并查询原生数据库确认记录不存在；没有 Hermes 推理/审批结论 |
| 操作台账参数边界 | 缺 agent、非法 params、缺 session 均先复现错误 reservation，修复后通过 | 校验在持久 reservation 前执行；原生结果未知仍保留 uncertain 并阻止未确认停机 |

日志均位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch`。详细阶段记录见 `session-daemon-go/VALIDATION.md`。

已补齐：事件保留容量/期限、清理后序号水位、重开清理、状态与回放范围一致性；持久操作 receipt 与只读查询；Python 丢响应恢复；严格远程删除验收。事件限额是逻辑字节预算，不是 SQLite 物理文件硬上限，且不覆盖 operations 台账。

仍未验收：操作台账保留策略、原生不确定结果对账与晚创建孤儿回收；完整原生能力/环境矩阵（Hermes/Grok/WSL、Windows Codex 工具沙箱）；剩余故障边界；全部测试资源清理；匹配安装/服务发布套件及独立审查。页面性能未由日志延迟测试证明。8 小时长稳、生产切换和回滚按用户要求暂缓。

早期后端测试曾在 collection 隔离修复前导入默认 App，其是否影响生产尚未核实，因此不作“历史测试从未接触生产”的保证。

---

## 1. 协议动作与控制面等价清单 (Parity Matrix)

根据 `internal/parity/manifest.json` 与原生 Python Session Daemon 契约对齐：

### 1.1 核心控制动作 (Control Actions)
| Action | 对应 Go 实现位置 | 状态判定与关键机制 | 测试与验证文件 |
|---|---|---|---|
| `daemon.handshake` | `internal/protocol/daemon.go` | 比较 `secret`，验证通过注册已认证会话，返回 `daemon.handshake.result` | `protocol/daemon_test.go` |
| `daemon.status` | `internal/protocol/daemon.go` | 聚合所有 Session WAL 权威快照、Connector 列表、Runtime 状态及 ModelConfig 信息 | `protocol/daemon_test.go`, `tests/test_go_daemon_bridge_integration.py` |
| `daemon.shutdown` | `internal/protocol/daemon.go` | 检查是否有 `running`/`waiting_approval` 会话，若有且未强制则拒绝停机；退出时调用 `AddCloser` 清理全局资源 | `protocol/shutdown_test.go` |
| `session.sync` | `internal/protocol/sync.go` | 支持游标回放、原子订阅挂接、`replay.batch_limit=128` 与分页接续 | `protocol/sync_test.go` |
| `session.create` | `internal/protocol/control.go` | 分发给对应 Agent Runtime 的 `Create`，创建全新原生会话并绑定 ID | `runtime/hermes/adapter_test.go`, `runtime/codex/adapter_test.go` |
| `session.spawn` | `internal/protocol/control.go` | 分发给对应 Agent Runtime 的 `Spawn`，恢复既有持久会话；若 `runtime_control==true` 自动绑定 Agent 控制面 | `protocol/connector_test.go`, `runtime/remote/real_remote_test.go` |
| `session.observe_status` | `internal/protocol/control.go` | 监听会话状态变化并订阅 | `protocol/control.go` |
| `runtime.request` | `internal/protocol/control.go` | 针对特定 Runtime 发起底层原生查询 | `protocol/query_test.go` |
| `runtime.disconnect` | `internal/runtime/disconnect_action.go` | 先上锁设置 `reloading` 屏障防止并发竞态，遍历目标会话权威状态，非 `idle` 坚决拒绝；平滑释放底层连接，若报错则保留映射并穿透返回 | `runtime/disconnect_action_test.go`, `tests/test_go_daemon_disconnect_integration.py` |
| `model_config.plan` | `internal/protocol/modelconfig.go` | 规划配置变更，计算差异与 hash | `protocol/modelconfig_test.go` |
| `model_config.apply` | `internal/protocol/modelconfig.go` | 原子写入配置文件并生成备份 | `protocol/modelconfig_test.go`, `tests/test_go_daemon_model_config_integration.py` |
| `model_config.reload` | `internal/protocol/modelconfig.go` | 在所属 Agent 会话空闲时触发重载；若会话繁忙则安全 defer | `protocol/modelconfig_test.go` |

### 1.2 会话控制动作 (Session Actions)
- `session.send`、`session.compact`、`session.review`、`session.steer`、`session.interrupt`、`session.approve`、`session.settings`、`session.resize`、`session.disconnect`、`session.history_page`、`session.delete`、`session.rename`、`session.close`、`session.models`、`session.commands`、`session.model.read`、`session.model.set`、`session.reasoning.set`、`session.approval.read`、`session.approval.set`
- 全部动作均由 `internal/protocol/control.go` 分发至 `internal/runtime` 各 Adapter（Codex、Hermes、Grok、PTY）。

---

## 2. 运行时覆盖情况 (Runtimes)

| 运行时类型 | 实现包位置 | 本机支持 (Windows) | 远程支持 (Debian / SSH) | 状态 |
|---|---|---|---|---|
| `pty` | `internal/runtime/pty` | Windows ConPTY (UTF-16/ANSI/STARTF_USESTDHANDLES) | Linux POSIX PTY (/bin/sh, resize, raw I/O) | 已通过真实 PTY 集成测试 |
| `codex` | `internal/runtime/codex` | 本机 app-server stdio JSON-RPC | - | 已通过真实生命周期验证 |
| `codex-ssh` | `internal/runtime/remote` + `codex` | - | OpenSSH 进程管道连接 `~/.vite-plus/bin/codex` | 已通过 Debian 192.168.1.100 实测 |
| `hermes` | `internal/runtime/hermes` | 本机官方启动器 / 插件 Gateway | - | 已通过真实持久化与多进程隔离验证 |
| `ssh` (remote hermes) | `internal/runtime/remote` + `hermes` | - | OpenSSH 进程管道启动 `hermes gateway --jsonrpc` | 已通过 Debian 192.168.1.100 实测 |
| `grok` | `internal/runtime/grok` | 本机 ACP / stdio 协议适配 | - | 已通过全量单测与协议验证 |
| `grok-ssh` | `internal/runtime/remote` + `grok` | - | OpenSSH 管道启动 Grok ACP | 已通过配置签名与 Multiplex 单测 |

---

## 3. 真实环境验收证据 (Real Environmental Acceptance)

### 3.1 远端 Debian 测试机 (`192.168.1.100:22`) 实测
执行用例：`session-daemon-go/internal/runtime/remote/real_remote_test.go`
- **RemoteCodex 测试**：通过 OpenSSH 真实拉起 `/home/luwei/.vite-plus/bin/codex app-server`，创建会话，执行工作区 allowlist 校验，并完成 `session.delete` 物理销毁。测试结果：**PASS (0.98s)**。
- **RemoteHermes 测试**：通过 OpenSSH 真实拉起 `/home/luwei/.hermes/hermes-agent/venv/bin/python -m tui_gateway.entry`，建立 JSON-RPC 网关并等待 `gateway.ready`，创建会话并完成物理注销。测试结果：**PASS (2.24s)**。

### 3.2 本地 Windows 环境实测
- **单元测试套件**：`go test ./... -count=1` —— **16 个包全部通过（0 failure）**。
- **代码静态合规**：`go vet ./...` —— **Clean (0 warning)**。
- **跨语言集成测试**：针对新编译的 `.runtime/astrorder-sessiond.exe` 执行 Python 测试：
  - `test_go_daemon_bridge_integration.py` —— **PASS**
  - `test_go_daemon_grok_integration.py` —— **PASS**
  - `test_go_daemon_model_config_integration.py` —— **PASS**
  - `test_go_daemon_pty_integration.py` —— **PASS**
  - `test_go_daemon_disconnect_integration.py` —— **PASS**

---

## 4. 生产切换状态与红线防护

当前生产环境尚未执行切流。`scripts/switch_to_go_daemon.py` 已设立严格门禁：
1. 必须提供小内核密钥以通过 authenticated probe 读取权威状态；
2. 若存在 `running`、`waiting_approval` 或非 `idle` 的活跃会话，脚本自动终止并输出活跃会话列表；
3. 优雅停止旧内核确认退出后，才启动 Go 小内核，并对 Go 小内核发送 `daemon.status` 进行握手与 epoch 对账，杜绝旧内核未退或新内核未就绪引发的中断。
