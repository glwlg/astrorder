from __future__ import annotations

import sys
from types import SimpleNamespace
from typing import ClassVar

import pytest

from astrorder import terminal_service


class FakePty:
    spawned: ClassVar[list[dict[str, object]]] = []

    def __init__(self, cols: int, rows: int) -> None:
        self.cols = cols
        self.rows = rows

    def spawn(self, appname: str, cmdline=None, cwd=None, env=None) -> bool:
        self.spawned.append({"appname": appname, "cmdline": cmdline, "cwd": cwd, "env": env})
        return True


@pytest.mark.asyncio
async def test_windows_terminal_overrides_inherited_dumb_terminal(monkeypatch, tmp_path) -> None:
    FakePty.spawned.clear()
    monkeypatch.setattr(terminal_service.sys, "platform", "win32")
    monkeypatch.setattr(terminal_service, "find_shell", lambda: "C:/Program Files/PowerShell/7/pwsh.exe")
    monkeypatch.setitem(sys.modules, "winpty", SimpleNamespace(PTY=FakePty))
    monkeypatch.setenv("TERM", "dumb")
    monkeypatch.setenv("COLORTERM", "legacy")
    monkeypatch.setenv("ASTRORDER_TEST_MARKER", "preserved")

    session = terminal_service.TerminalSession(workspace=str(tmp_path))
    await session.start()

    assert len(FakePty.spawned) == 1
    raw_environment = FakePty.spawned[0]["env"]
    assert isinstance(raw_environment, str)
    environment = dict(item.split("=", 1) for item in raw_environment.rstrip("\0").split("\0"))
    assert environment["TERM"] == "xterm-256color"
    assert environment["COLORTERM"] == "truecolor"
    assert environment["ASTRORDER_TEST_MARKER"] == "preserved"
