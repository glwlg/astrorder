# 大小内核 Phase 2：App Bridge 与 Runtime Ownership

## 已完成范围

Phase 1 的 Session Daemon 已不再只是 WAL 原型。大内核现在可以在**显式开启**的情况下连接 daemon、持久化确认位置、回放规范化事件，并使用独立的 loopback IPC socket 发送 runtime control 请求。

产品默认关闭 bridge 与所有 runtime ownership 开关；旧 App-owned runtime 不做隐式迁移。当前 production 配置已显式 opt-in，daemon 直接持有 Codex、Hermes、SSH Hermes 与 PTY runtime，App Server 只保留 Store/UI 投影、catalog reader 与编排职责。

## 新增协议保证

### Daemon identity 与 checkpoint

- 每个 `SessionDaemon` 启动时产生稳定的 `daemon_id`；`daemon.status.result` 与 `session.sync.result` 都携带该值。
- SQLite 新表 `daemon_checkpoints` 以 `(daemon_id, session_id)` 保存大内核已持久化的最高 `seq_id`。
- daemon 自身重启后，新的 `daemon_id` 绝不会把新的 `seq_id=1` 错认成旧 WAL 的已确认帧。
- checkpoint 仅在 `ControlService.accept_connector_event(...)` 成功持久化 canonical event 后推进。

### Replay 与 live ordering

- App Server 先请求 `daemon.status`，将 daemon 当前 session 集与该 daemon epoch 的 checkpoint 合并后发送 `session.sync`。
- daemon 在成功发送 `session.sync.result` 后才将 socket 订阅为 live client；同步前不会推送实时帧。
- replay 使用 `event: "connector.event"`，payload 保持已有 connector Event wire shape，因此沿用既有 schema、identity 校验和 event persistence。
- replay 中 `overflow=true` 的 session 不会处理 retained tail，也不会推进 checkpoint。完整 native snapshot 尚未实现前，该 session 的后续 live tail 同样被隔离，避免不完整历史被误投影。

### Runtime control registry

`SessionDaemon` 提供显式 registry，而不是通过 agent type 字符串拼接或 if/else 猜测 adapter：

- `register_runtime(agent_type, adapter)` 要求 adapter 提供 async `spawn` 与 `command`。
- `session.spawn` 将 opaque `session_id` 绑定到一个已注册 adapter。
- `session.send`、`session.interrupt`、`session.approve` 只能路由到该 exact session binding；未知或非 daemon-owned session 被拒绝。
- `DaemonBridge.request_control(...)` 使用独立的 one-shot loopback socket，不干扰持续的 replay/live subscription，也不允许调用方覆盖 IPC `request_id`。

该 registry 是迁移真实 runtime adapter 的 seam；本阶段的测试 adapter 是 inert fixture，不声称连接了 Codex 或 Hermes。

## 配置与生命周期

新增 Settings：

- `ASTRORDER_SESSION_DAEMON_ENABLED`：默认 `false`
- `ASTRORDER_SESSION_DAEMON_ENDPOINT`：默认 `ws://127.0.0.1:30009`
- `ASTRORDER_SESSION_DAEMON_SECRET`：可选的独立 IPC secret；不能复用浏览器或 connector secret。
- `ASTRORDER_SESSION_DAEMON_REQUEST_TIMEOUT`：默认 `30` 秒，覆盖 native cold start；限制为 `(0, 300]`。
- `ASTRORDER_DAEMON_CODEX_ENABLED` / `ASTRORDER_DAEMON_HERMES_ENABLED` / `ASTRORDER_DAEMON_SSH_ENABLED` / `ASTRORDER_DAEMON_PTY_ENABLED`：均默认 `false`。

启用后，FastAPI lifespan 创建 `DaemonBridge.run(...)`。关闭 App Server 时先发停止信号、短等待后取消 bridge task；不会启动、停止或向 daemon 发送 runtime termination 命令。

若 daemon 配置了 secret，所有 socket 在 `daemon.status`、`session.sync` 或 runtime control 前必须完成 `daemon.handshake`。runtime registry 本身也拒绝无 secret 的注册，因此 CLI 或库调用都不能在未认证 IPC 上挂载可执行 adapter。

## Phase 2B-1：Codex transport owner（已实现，未生产启用）

- `CodexDaemonRuntime` 只依赖 Codex app-server transport 与 daemon emitter；它不 import App Server 的 Store、ControlService、附件管理或 `CodexConnection`。
- 显式 `session.spawn` 后，daemon 启动其直接拥有的 Codex app-server、执行 `initialize` / `initialized` / `thread/resume`，并将该 native thread 绑定到此 runtime。
- `session.send` 映射为 native `turn/start`；`session.interrupt` 只作用于 exact active turn；`session.approve` 只接受该 daemon session 已记录的 approval ID。完成状态仍等待 native notification，不把 interrupt write 误报为完成。
- Codex native notification 以 `codex.notification` 写入 session WAL，包含 stable `agent_id`。App-side `CodexNativeFrameRouter` 按 exact agent ID 与 native `threadId` 路由；未知 agent、跨 session frame 或未注册 handler 都不会推进 checkpoint。
- `create_session_daemon(..., codex_config=...)` 和 CLI `--enable-codex` 是显式 opt-in；启用 Codex runtime 时 daemon secret 必须来自 `ASTRORDER_SESSION_DAEMON_SECRET`，不接受命令行 secret。

## Phase 2B-2：Hermes、SSH、PTY 与 connector projection

- daemon connector endpoint 使用独立 connector secret，接收 Hermes plugin/SSH reverse tunnel 的 canonical event；agent/session identity 未绑定前仅做有界 pending buffering，绑定后写入 exact control/session WAL。
- `DaemonHermesController` 与 `DaemonSshController` 使用 opaque runtime-control ID；send/interrupt/history/disconnect 必须先建立 exact native session binding。
- Hermes/SSH command ID贯穿 App command、native controller 与 `hermes.command_complete` WAL frame；App projector只完成 exact `(agent_id, session_id, command_id)`，未知 frame 不推进 checkpoint。
- `session.history_page` 在 daemon 内调用 local/remote native reader，App不读取远程文件路径，也不把 unsupported伪装为空历史。
- SSH runtime registry按 exact connection ID隔离 child与disconnect scope；SSH永不创建或fallback到local PTY。
- PTY browser relay只接受明确 local daemon-owned target；input/resize/interrupt/close均由daemon runtime处理。
- Hermes/SSH attachments由App通过AttachmentManager解析已登记ID，IPC仅携带path-free base64 payload；daemon按MIME和总大小二次验证后调用原生byte APIs。默认10 MiB附件由16 MiB authenticated loopback frame承载。
- Hermes/SSH rename使用显式`session.rename` action；daemon按durable native ID resume到runtime handle，执行title set并要求native readback一致后，App才更新Store cache。

## 验证

从 `backend/`：

```text
uv run pytest tests/test_session_daemon.py tests/test_daemon_bridge.py tests/test_daemon_config.py tests/test_daemon_codex_runtime.py tests/test_daemon_codex_projection.py -q
```

当前全量结果：`291 passed, 1 skipped`；skip为显式 opt-in的真实Codex测试，另有2条已知 upstream TestClient/AnyIO deprecation warnings。daemon-focused Ruff、Python compile、TypeScript/Vite build与`git diff --check`均通过。

覆盖：

- WAL 增量 replay、overflow、daemon epoch；
- WebSocket sync/status/live ordering；
- SQLite checkpoint 重开与 daemon-id 隔离；
- 大内核重启后的 replay only-unacknowledged；
- overflow 后拒绝 live tail；
- bridge 长连接的 live event projection；
- Settings 与 FastAPI lifespan enable/stop；
- registry 驱动的 spawn/send/interrupt/approve 控制与 one-shot control client。
- secret-protected IPC handshake 和 runtime registry secret gate；
- daemon-owned fake Codex app-server 的 initialize/resume/turn/WAL ownership；
- authenticated bridge control → Codex notification WAL → App-side agent/thread router → checkpoint 的完整 mock transport vertical slice。

此外：

```text
uv run ruff check src/astrorder/daemon tests/test_session_daemon.py tests/test_daemon_bridge.py tests/test_daemon_config.py tests/test_daemon_codex_runtime.py tests/test_daemon_codex_projection.py
uv run ruff check --select I src/astrorder/main.py src/astrorder/config.py src/astrorder/models.py src/astrorder/store.py
git diff --check -- <Phase 2 targets>
```

均通过。

## 真实隔离验收

- 真实 `codex-cli 0.150.0`：ephemeral thread create、turn、control disconnect、daemon继续运行、WAL `turn/completed` replay、reattach均通过。
- 真实 Chrome + isolated FastAPI/SQLite/static/workspace：UI创建daemon-owned Codex session并发送临时marker任务；App Server离线时marker完成且daemon listener存活；重启后后台自动恢复Codex projection，同一页面显示user/tool/assistant三条记录并回到idle。
- Browser screenshot：`.runtime/test-results/native-codex-daemon-restart.png`。
- 所有临时listener与owned runtime已authenticated shutdown并确认关闭；production `30001/30009`未重启或启用。

## Production验收

- daemon-owned Codex完成真实prompt、completion、history、App-only restart replay和delete。
- local Hermes与SSH Hermes完成真实prompt、completion、history、model/reasoning/approval控制、path-free附件读取、rename native readback和delete；测试使用disposable native sessions。
- PTY完成真实browser WebSocket output、resize、close和binding释放。
- Production已在备份与rollback点后显式启用所有ownership开关；产品默认值仍保持关闭。
- Production已启用持久化模型tier路由：Hermes small→large→small与Codex small真实prompt均completed；今日验收ledger分别为7与1 cost units。