"""Malformed continuation metadata must not advance durable checkpoints."""
import pytest
from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.daemon.bridge.client import DaemonBridgeError
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store

@pytest.mark.asyncio
@pytest.mark.parametrize("mutation", [
    {"has_more": "false"}, {"has_more": False, "next_seq_id": 1, "max_seq_id": 2},
    {"has_more": True, "next_seq_id": 2, "max_seq_id": 3}, {"has_more": True, "next_seq_id": True},
])
async def test_invalid_page_is_rejected_before_projection(tmp_path, mutation):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/invalid.db", auto_connect_local_hermes=False)
    store = Store(settings)
    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            if action == "daemon.status":
                return {"daemon_id": "d", "sessions": {"s": {"status": "idle"}}, "connectors": []}
            return {"daemon_id": "d", "sessions": {"s": {
                "frames": [{"session_id": "s", "seq_id": 1, "timestamp": 1.0,
                            "event": "native.test", "payload": {}}],
                "overflow": False, "min_seq_id": 1, "max_seq_id": 1,
                "next_seq_id": 1, "has_more": False, "status": "idle", **mutation,
            }}}
    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge.register_native_frame_handler("native.test", lambda *args: True)
    try:
        with pytest.raises(DaemonBridgeError):
            await bridge._synchronize(None)
        assert store.get_daemon_checkpoint("d", "s") == 0
    finally:
        store.close()


@pytest.mark.asyncio
@pytest.mark.parametrize("defect", ["missing_session", "missing_metadata", "changed_boundary", "short_final_page"])
async def test_incomplete_continuation_cannot_finish_sync(tmp_path, defect):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/continuation.db", auto_connect_local_hermes=False)
    store = Store(settings)

    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            if action == "daemon.status":
                return {"daemon_id": "d", "sessions": {"s": {"status": "idle"}}, "connectors": []}
            after = fields["sessions"]["s"]
            page = {"frames": [{"session_id": "s", "seq_id": after + 1,
                                "event": "native.test", "payload": {}}],
                    "overflow": False, "has_more": after == 0, "min_seq_id": 1,
                    "max_seq_id": 2, "next_seq_id": after + 1, "status": "idle"}
            if after:
                if defect == "missing_session":
                    return {"daemon_id": "d", "sessions": {}}
                if defect == "missing_metadata":
                    page.pop("has_more")
                if defect == "changed_boundary":
                    page["max_seq_id"] = 3
                if defect == "short_final_page":
                    page["frames"] = []
                    page["next_seq_id"] = 1
            return {"daemon_id": "d", "sessions": {"s": page}}

    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge.register_native_frame_handler("native.test", lambda *args: True)
    try:
        with pytest.raises(DaemonBridgeError):
            await bridge._synchronize(None)
        assert store.get_daemon_checkpoint("d", "s") == 1
    finally:
        store.close()
