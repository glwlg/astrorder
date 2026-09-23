import pytest

from astrorder.connections import ConnectionError
from astrorder.native.controls import runtime_rpc


def test_runtime_rpc_rejects_daemon_proxy_without_explicit_control_interface():
    class Connections:
        local = type("Proxy", (), {"daemon_owned": True})()

        def get_runtime_by_agent_id(self, _agent_id):
            return self.local

    with pytest.raises(ConnectionError, match="显式控制接口"):
        runtime_rpc(Connections(), "daemon-hermes")
