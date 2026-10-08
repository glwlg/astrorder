import pytest

from astrorder.connections import ConnectionError
from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.daemon.clients.ssh.control import DaemonSshController


@pytest.mark.parametrize("blocked", [False, True])
def test_closed_transport_reconnect_requires_daemon_safe_release(blocked):
    class Bridge:
        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, fields))
            if len(self.calls) == 1:
                raise DaemonBridgeError("hermes adapter is closed")
            if action == "runtime.disconnect":
                assert fields == {"agent_type": "ssh", "connection_id": "remote-a"}
                if blocked:
                    raise DaemonBridgeError("ssh has unconfirmed sessions")
                return {"result": {"disconnected": True, "released_sessions": []}}
            assert action == "session.spawn"
            return {"result": {"agent_id": "ssh-hermes-remote-a", "connection_id": "remote-a"}}

    bridge = Bridge()
    controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings={"host": "test"})
    if blocked:
        with pytest.raises(ConnectionError):
            controller.start()
        assert [a for a, _ in bridge.calls] == ["session.spawn", "runtime.disconnect"]
    else:
        assert controller.start()["alive"] is True
        assert [a for a, _ in bridge.calls] == ["session.spawn", "runtime.disconnect", "session.spawn"]
        assert bridge.calls[0][1] == bridge.calls[2][1]


def test_unknown_start_failure_is_not_retried():
    class Bridge:
        calls = 0

        async def request_control(self, action, fields):
            self.calls += 1
            raise DaemonBridgeError("context deadline exceeded")

    bridge = Bridge()
    controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings={"host": "test"})
    with pytest.raises(ConnectionError):
        controller.start()
    assert bridge.calls == 1
