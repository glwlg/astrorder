"""Spool mode: report Hermes activity without a connector secret.

User-launched Hermes (the desktop app, the CLI) writes ``state.db`` without
telling Astrorder. In this mode the plugin drops one small JSON file per
lifecycle hook into ``<hermes home>/astrorder-observer/events``; the Astrorder
session daemon watches that directory and moves the session's last-activity
time. Only stdlib is used and no hook ever raises into Hermes.

File format (same as the Codex observer spool):
``{"id": <32 hex>, "session_id", "event", "observed_at": <unix seconds>, "turn_id"?}``
"""

from __future__ import annotations

import json
import os
import threading
import time
import uuid
from pathlib import Path
from typing import Any

MAX_FILES = 2000
MAX_AGE_SECONDS = 24 * 60 * 60
TOOL_THROTTLE_SECONDS = 2.0
_PRUNE_EVERY = 200

# Hermes hook name -> the event name the Go watcher understands.
_HOOK_EVENTS = {
    "on_session_start": "SessionStart",
    "on_session_end": "SessionEnd",
    "pre_llm_call": "UserPromptSubmit",
    "post_llm_call": "Stop",
    "pre_tool_call": "PreToolUse",
    "post_tool_call": "PostToolUse",
    "subagent_start": "SubagentStart",
    "subagent_stop": "SubagentStop",
}
_TOOL_EVENTS = {"PreToolUse", "PostToolUse"}


def spool_dir() -> Path:
    """Return the spool directory for the active Hermes home."""
    try:
        from hermes_constants import get_hermes_home

        home = Path(get_hermes_home())
    except Exception:  # noqa: BLE001 - fall back to the documented defaults
        configured = os.environ.get("HERMES_HOME", "").strip()
        if configured:
            home = Path(os.path.expanduser(os.path.expandvars(configured)))
        elif os.name == "nt":
            base = os.environ.get("LOCALAPPDATA", "").strip() or str(Path.home() / "AppData" / "Local")
            home = Path(base) / "hermes"
        else:
            home = Path.home() / ".hermes"
    return home / "astrorder-observer" / "events"


class SpoolBridge:
    """Writes one spool file per Hermes lifecycle hook."""

    def __init__(self, ctx: Any, directory: Path | None = None):
        self.ctx = ctx
        self.directory = directory or spool_dir()
        self._lock = threading.Lock()
        self._last_tool: dict[str, float] = {}
        self._writes = 0

    def register(self) -> SpoolBridge:
        for hook, event in _HOOK_EVENTS.items():
            self.ctx.register_hook(hook, self._handler(event))
        return self

    def _handler(self, event: str):
        def handle(session_id: str = "", **kwargs: Any) -> None:
            try:
                self.record(event, session_id, str(kwargs.get("turn_id") or ""))
            except Exception:  # noqa: BLE001 - observing must never break the Agent
                pass

        return handle

    def record(self, event: str, session_id: str, turn_id: str = "", now: float | None = None) -> bool:
        session_id = (session_id or "").strip()
        if not session_id or len(session_id) > 256:
            return False
        stamp = time.time() if now is None else now
        if event in _TOOL_EVENTS:
            with self._lock:
                last = self._last_tool.get(session_id, 0.0)
                if stamp - last < TOOL_THROTTLE_SECONDS:
                    return False
                if len(self._last_tool) > 4096:
                    self._last_tool.clear()
                self._last_tool[session_id] = stamp
        row: dict[str, Any] = {
            "id": uuid.uuid4().hex,
            "session_id": session_id,
            "event": event,
            "observed_at": stamp,
        }
        if turn_id and len(turn_id) <= 256:
            row["turn_id"] = turn_id
        self.directory.mkdir(parents=True, exist_ok=True)
        target = self.directory / f"{row['id']}.json"
        temporary = self.directory / f".{row['id']}.tmp"
        temporary.write_text(json.dumps(row, separators=(",", ":")), encoding="utf-8")
        os.replace(temporary, target)
        with self._lock:
            self._writes += 1
            due = self._writes % _PRUNE_EVERY == 0
        if due:
            self.prune(stamp)
        return True

    def prune(self, now: float | None = None) -> None:
        """Drop expired files, then the oldest beyond MAX_FILES."""
        stamp = time.time() if now is None else now
        files = []
        try:
            for entry in os.scandir(self.directory):
                if entry.is_file() and entry.name.endswith(".json"):
                    files.append((entry.stat().st_mtime, entry.path))
        except OSError:
            return
        files.sort()
        excess = max(0, len(files) - MAX_FILES)
        for index, (mtime, path) in enumerate(files):
            if index < excess or stamp - mtime > MAX_AGE_SECONDS:
                try:
                    os.remove(path)
                except OSError:
                    pass
