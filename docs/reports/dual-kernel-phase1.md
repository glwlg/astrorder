# 大小内核 Phase 1：Session Daemon 原型

## 范围

已完成 `docs/design/dual-kernel-architecture.md` 中 Phase 1 的可运行原型；本阶段不迁移 Hermes、Codex、SSH 或 PTY 的进程所有权，因此不会改变现有 Astrorder 会话的运行方式。

实现位置：

- `backend/src/astrorder/daemon/session_daemon.py`
- `backend/src/astrorder/daemon/__init__.py`
- `backend/tests/test_session_daemon.py`

## 已实现

- 独立可执行入口：`python -m astrorder.daemon.session_daemon`。
- 仅允许绑定 loopback 地址（`127.0.0.1`、`::1`、`localhost`）；默认端口为 `30009`。
- 每会话内存 WAL 环形缓冲，默认容量 `2000` 帧。
- 每会话从 `1` 开始的单调 `seq_id`。
- `session.sync`：按大内核最后确认的 seq 回放缺失帧。
- 明确 `overflow`：若 checkpoint 早于 retained WAL 的最早序号，返回 retained tail 且标记溢出，调用方必须进行原生全量刷新。
- `daemon.status`：返回 daemon 当前持有会话的权威状态和 seq 水位。
- 已同步的 WebSocket 客户端可接收后续实时 Frame Envelope。

## IPC 形状

请求：

```json
{"action":"session.sync","request_id":"sync-1","sessions":{"session-a":42}}
```

响应：

```json
{
  "action":"session.sync.result",
  "request_id":"sync-1",
  "sessions": {
    "session-a": {
      "frames": [],
      "overflow":false,
      "min_seq_id":1,
      "max_seq_id":42,
      "status":"running"
    }
  }
}
```

实时帧直接采用设计规范的 Envelope：

```json
{"session_id":"session-a","seq_id":43,"timestamp":0.0,"event":"token_chunk","payload":{"delta":"..."}}
```

## 验证

在 `backend/` 执行：

- `uv run pytest tests/test_session_daemon.py -q` → `3 passed`
- `uv run ruff check src/astrorder/daemon tests/test_session_daemon.py` → `All checks passed!`
- `.venv/Scripts/python.exe -m astrorder.daemon.session_daemon --help` → exit 0
- 在隔离端口 `127.0.0.1:30109` 启动 daemon，通过真实 WebSocket 收到：
  `{"action":"daemon.status.result","request_id":"probe-1","sessions":{}}`
  随后只终止该测试 daemon，并确认没有 `LISTENING` socket。
- `uv run pytest -q` → `174 passed, 2 upstream TestClient/AnyIO deprecation warnings`

## 尚未迁移（Phase 2+）

- Daemon 尚未成为 Codex app-server、Hermes gateway、SSH tunnel、PTY 的父进程。
- App Server 尚未将 runtime 事件写入 daemon，亦未在启动时通过 `session.sync` 持久化/回放 seq checkpoint。
- 生产端口 `30009` 未自动启动 daemon；当前生产 `30001` 的既有会话不会被此原型改变。
- `session.spawn`、`session.send`、`session.interrupt`、`session.approve` 的 runtime 控制委托仍待 Phase 2 完成。
