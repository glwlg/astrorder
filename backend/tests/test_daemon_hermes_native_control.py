import pytest

from astrorder.daemon.hermes_native_control import execute_native_session_control
from astrorder.daemon.session_daemon import DaemonProtocolError


def test_daemon_hermes_native_control_reads_catalog_model_and_effort():
    class Controller:
        def _rpc(self, method, params):
            if method == "model.options":
                return {"result": {"providers": [{"slug": "provider-a", "name": "Provider A", "authenticated": True, "models": ["model-a"]}]}}
            if method == "session.resume":
                return {"result": {"session_id": "handle-a", "info": {"model": "model-a", "provider": "provider-a", "branch": "main"}}}
            if method == "config.get":
                assert params == {"session_id": "handle-a", "key": "reasoning"}
                return {"result": {"value": "medium"}}
            raise AssertionError((method, params))

    controller = Controller()
    catalog = execute_native_session_control(
        controller, "session.models", {"session_id": "native-session"}
    )
    current = execute_native_session_control(
        controller, "session.model.read", {"session_id": "native-session"}
    )

    assert catalog["items"] == [
        {"provider": "provider-a", "model": "model-a", "label": "Provider A · model-a"}
    ]
    assert current == {
        "status": "idle",
        "model": "model-a",
        "provider": "provider-a",
        "branch": "main",
        "effort": "medium",
    }


def test_daemon_hermes_native_control_rejects_unknown_action():
    with pytest.raises(DaemonProtocolError, match="unsupported"):
        execute_native_session_control(
            object(), "session.arbitrary-rpc", {"session_id": "native-session"}
        )
