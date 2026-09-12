from types import SimpleNamespace

import pytest
from websockets.exceptions import ConnectionClosedError

from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
async def test_daemon_connector_network_reset_is_a_normal_disconnect():
    class Socket:
        request = SimpleNamespace(
            path="/ws/v1/connector",
            headers={"Authorization": "Bearer connector-secret"},
        )

        def __aiter__(self):
            return self

        async def __anext__(self):
            raise ConnectionClosedError(None, None)

        async def close(self, code, reason):
            raise AssertionError((code, reason))

    daemon = SessionDaemon(
        secret="daemon-secret",
        connector_secret="connector-secret",
    )

    await daemon._serve_socket(Socket())
