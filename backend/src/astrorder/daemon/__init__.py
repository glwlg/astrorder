"""App-side IPC clients and projections for the Go Session Daemon."""
import importlib
from typing import Any

_SUBMODULE_MAP = {
    # bridge & projections
    "bridge": "astrorder.daemon.bridge.client",
    "codex_projection": "astrorder.daemon.bridge.codex_projection",
    "hermes_projection": "astrorder.daemon.bridge.hermes_projection",
    "hermes_compaction_projection": "astrorder.daemon.bridge.hermes_compaction_projection",
    # App clients only; runtime implementations live in session-daemon-go.
    "codex_control": "astrorder.daemon.clients.codex.control",
    "hermes_control": "astrorder.daemon.clients.hermes.control",
    "ssh_control": "astrorder.daemon.clients.ssh.control",
    "terminal_relay": "astrorder.daemon.clients.pty.relay",
}

def __getattr__(name: str) -> Any:
    if name in _SUBMODULE_MAP:
        return importlib.import_module(_SUBMODULE_MAP[name])
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
