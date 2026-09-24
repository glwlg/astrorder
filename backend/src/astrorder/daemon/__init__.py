"""Long-lived session daemon primitives for Astrorder's dual-kernel design."""
import importlib
from typing import Any

_SUBMODULE_MAP = {
    # bridge & projections
    "bridge": "astrorder.daemon.bridge.client",
    "codex_projection": "astrorder.daemon.bridge.codex_projection",
    "hermes_projection": "astrorder.daemon.bridge.hermes_projection",
    "hermes_compaction_projection": "astrorder.daemon.bridge.hermes_compaction_projection",
    # runtimes/codex
    "codex_runtime": "astrorder.daemon.runtimes.codex.runtime",
    "codex_control": "astrorder.daemon.runtimes.codex.control",
    "codex_desktop": "astrorder.daemon.runtimes.codex.desktop",
    "remote_codex_runtime": "astrorder.daemon.runtimes.codex.remote",
    # runtimes/hermes
    "hermes_runtime": "astrorder.daemon.runtimes.hermes.runtime",
    "hermes_control": "astrorder.daemon.runtimes.hermes.control",
    "hermes_native_control": "astrorder.daemon.runtimes.hermes.native_control",
    # runtimes/grok
    "grok_runtime": "astrorder.daemon.runtimes.grok.runtime",
    "remote_grok_runtime": "astrorder.daemon.runtimes.grok.remote",
    # runtimes/ssh
    "ssh_runtime": "astrorder.daemon.runtimes.ssh.runtime",
    "ssh_control": "astrorder.daemon.runtimes.ssh.control",
    # runtimes/pty
    "pty_runtime": "astrorder.daemon.runtimes.pty.runtime",
    "terminal_relay": "astrorder.daemon.runtimes.pty.relay",
}

def __getattr__(name: str) -> Any:
    if name in _SUBMODULE_MAP:
        return importlib.import_module(_SUBMODULE_MAP[name])
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
