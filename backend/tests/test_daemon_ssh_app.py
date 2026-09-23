from __future__ import annotations

from astrorder.config import Settings
from astrorder.connections import ConnectionController
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


def test_connection_controller_uses_daemon_ssh_factory_without_stopping_proxy_on_app_shutdown(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/daemon-ssh-app.sqlite3",
        connector_secret="connector-test",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    proxies = []

    class FakeDaemonSsh:
        daemon_owned = True
        supports_native_history = False
        supports_native_mutation = False
        metadata = None

        def __init__(self, row):
            self.connection_id = row["id"]
            self.agent_id = f"ssh-hermes-{self.connection_id}"
            self.runtime_id = self.agent_id
            self.source_id = f"hermes-ssh-{self.connection_id}"
            self.started = 0
            self.stopped = 0
            self.closed = 0

        def start(self):
            self.started += 1
            return self.snapshot()

        def stop(self):
            self.stopped += 1
            return self.snapshot()

        def close_for_app_shutdown(self):
            self.closed += 1

        def snapshot(self):
            return {
                "alive": True,
                "agent_id": self.agent_id,
                "runtime_id": self.runtime_id,
                "source_id": self.source_id,
            }

    def factory(row):
        proxy = FakeDaemonSsh(row)
        proxies.append(proxy)
        return proxy

    controller = ConnectionController(settings, store, daemon_ssh_factory=factory)
    row = controller.save_ssh({"host": "remote.example", "port": 22, "user": "operator"})
    try:
        snapshot = controller.connect_ssh(service, row["id"])

        assert proxies[0].started == 1
        assert proxies[0].stopped == 0
        assert proxies[0].agent_id in service._native_command_handlers
        assert proxies[0].agent_id not in service._native_history_handlers
        assert snapshot["ssh"]["items"][0]["state"] == "connecting"
        assert snapshot["ssh"]["items"][0]["daemon_mode"] is True
        store.upsert_agent(
            {
                "id": proxies[0].agent_id,
                "kind": "hermes",
                "name": "Daemon SSH Hermes",
                "status": "ready",
                "capabilities": ["chat", "events"],
                "limitation": None,
                "source_id": proxies[0].source_id,
                "connection_id": row["id"],
            }
        )
        projected = controller.snapshot(service)
        assert projected["ssh"]["items"][0]["state"] == "connected"

        controller.shutdown(service)
        assert proxies[0].closed == 1
        assert proxies[0].stopped == 0
    finally:
        store.close()


def test_daemon_ssh_factory_receives_saved_connection_display_name(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/daemon-ssh-display-name.db")
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    captured = {}

    class Proxy:
        daemon_owned = True
        agent_id = "ssh-hermes-fixture"
        runtime_id = agent_id
        metadata = None
        service = None
        store = None
        app_settings = None

        def start(self):
            return None

        def wait_gateway(self, timeout=20):
            return True

        def snapshot(self):
            return {"alive": True, "agent_id": self.agent_id, "runtime_id": self.runtime_id}

    def factory(row):
        captured.update(row)
        return Proxy()

    controller = ConnectionController(settings, store, daemon_ssh_factory=factory)
    row = controller.save_ssh(
        {
            "display_name": "Debian",
            "host": "debian.example",
            "port": 22,
            "user": "operator",
        }
    )
    try:
        controller.connect_ssh(service, row["id"])
        assert captured["display_name"] == "Debian"
        assert captured["settings"]["display_name"] == "Debian"
    finally:
        store.close()
