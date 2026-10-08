"""Paged daemon replay must be fully projected before live reception."""
import pytest
from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store

@pytest.mark.asyncio
async def test_bridge_reads_all_pages_at_fixed_boundary(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/state.db", auto_connect_local_hermes=False)
    store = Store(settings)
    calls = []
    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            if action == "daemon.status":
                return {"daemon_id": "d", "sessions": {"s": {"status": "idle"}}, "connectors": []}
            calls.append(fields)
            after = fields["sessions"]["s"]
            return {"daemon_id": "d", "sessions": {"s": {
                "frames": [{"session_id": "s", "seq_id": after + 1, "timestamp": 1.0, "event": "native.test", "payload": {}}],
                "overflow": False, "min_seq_id": 1, "max_seq_id": 3,
                "next_seq_id": after + 1, "has_more": after < 2, "status": "idle",
            }}}
    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge.register_native_frame_handler("native.test", lambda *args: True)
    try:
        report = await bridge._synchronize(None)
        assert report.replayed_frames == 3
        assert store.get_daemon_checkpoint("d", "s") == 3
        assert calls[1]["through"] == {"s": 3}
        assert calls[2]["sessions"] == {"s": 2}
    finally:
        store.close()


@pytest.mark.asyncio
async def test_bridge_batches_sessions_and_defers_live_until_all_batches_finish(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/batches.db", auto_connect_local_hermes=False)
    store = Store(settings)
    calls = []

    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            if action == "daemon.status":
                return {"daemon_id": "d", "sessions": {f"s-{i}": {"status": "idle"} for i in range(130)},
                        "connectors": [], "replay": {"batch_limit": 128, "explicit_handoff": True}}
            calls.append(fields)
            assert len(fields["sessions"]) <= 128
            return {"daemon_id": "d", "sessions": {sid: {
                "frames": [{"session_id": sid, "seq_id": 1, "timestamp": 1.0,
                            "event": "native.test", "payload": {}}],
                "overflow": False, "min_seq_id": 1, "max_seq_id": 1,
                "next_seq_id": 1, "has_more": False, "status": "idle",
            } for sid in fields["sessions"]}}

    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge.register_native_frame_handler("native.test", lambda *args: True)
    try:
        report = await bridge._synchronize(None)
        assert report.replayed_frames == 130
        assert [len(call["sessions"]) for call in calls] == [128, 2, 0]
        assert all(call["defer_live"] is True for call in calls[:-1])
        assert calls[-1] == {"sessions": {}, "defer_live": False}
        assert store.get_daemon_checkpoint("d", "s-129") == 1
    finally:
        store.close()
