from __future__ import annotations

import json
import os
import re
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from connectors.hermes import astrorder_hermes_plugin as plugin
from connectors.hermes.astrorder_hermes_plugin.spool import MAX_FILES, SpoolBridge

ID_PATTERN = re.compile(r"^[a-f0-9]{32}(?:[a-f0-9]{32})?$")


class FakeContext:
    def __init__(self):
        self.hooks: dict[str, object] = {}

    def register_hook(self, name, callback):
        self.hooks[name] = callback


class SpoolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR"))
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name) / "events"

    def rows(self):
        return [json.loads(p.read_text(encoding="utf-8")) for p in sorted(self.directory.glob("*.json"))]

    def test_register_without_secret_uses_spool_mode(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("ASTRORDER_CONNECTOR_SECRET", None)
            with patch.object(plugin, "__name__", plugin.__name__):
                bridge = plugin.register(FakeContext())
        self.assertIsInstance(bridge, SpoolBridge)

    def test_register_with_secret_keeps_connector_mode(self):
        env = {"ASTRORDER_CONNECTOR_SECRET": "x", "ASTRORDER_HERMES_AGENT_ID": "hermes-1",
               "ASTRORDER_HERMES_CONNECT_ON_REGISTER": "0"}
        with patch.dict(os.environ, env):
            bridge = plugin.register(FakeContext())
        self.assertIsInstance(bridge, plugin.HermesBridge)

    def test_hooks_write_files_the_go_watcher_accepts(self):
        ctx = FakeContext()
        SpoolBridge(ctx, self.directory).register()
        ctx.hooks["pre_llm_call"](session_id="20261011_abc", turn_id="t1", user_message="SECRET prompt")
        ctx.hooks["post_llm_call"](session_id="20261011_abc", user_message="SECRET", assistant_response="SECRET")
        rows = self.rows()
        self.assertEqual({r["event"] for r in rows}, {"UserPromptSubmit", "Stop"})
        for row in rows:
            self.assertRegex(row["id"], ID_PATTERN)
            self.assertEqual(row["session_id"], "20261011_abc")
            self.assertIsInstance(row["observed_at"], float)
            self.assertTrue((self.directory / f"{row['id']}.json").is_file())
        self.assertNotIn("SECRET", "".join(p.read_text(encoding="utf-8") for p in self.directory.glob("*")))

    def test_tool_hooks_are_throttled_but_lifecycle_hooks_are_not(self):
        bridge = SpoolBridge(FakeContext(), self.directory)
        self.assertTrue(bridge.record("PreToolUse", "s1", now=100.0))
        self.assertFalse(bridge.record("PostToolUse", "s1", now=100.5))
        self.assertTrue(bridge.record("PostToolUse", "s1", now=103.0))
        self.assertTrue(bridge.record("UserPromptSubmit", "s1", now=103.1))
        self.assertTrue(bridge.record("UserPromptSubmit", "s1", now=103.2))

    def test_invalid_session_ids_are_ignored_and_hooks_never_raise(self):
        bridge = SpoolBridge(FakeContext(), self.directory)
        self.assertFalse(bridge.record("Stop", ""))
        self.assertFalse(bridge.record("Stop", "x" * 257))
        blocker = Path(self.temporary.name) / "file"
        blocker.write_text("not a directory")
        broken = SpoolBridge(FakeContext(), blocker / "events")
        handler = broken._handler("Stop")
        handler(session_id="s1")  # must not raise

    def test_prune_removes_expired_then_oldest_over_cap(self):
        bridge = SpoolBridge(FakeContext(), self.directory)
        self.directory.mkdir(parents=True)
        old = self.directory / ("a" * 32 + ".json")
        old.write_text("{}")
        os.utime(old, (1, 1))
        for i in range(MAX_FILES + 3):
            path = self.directory / f"{i:032x}.json"
            path.write_text("{}")
            os.utime(path, (1_000_000 + i, 1_000_000 + i))
        bridge.prune(now=1_000_000 + MAX_FILES + 10)
        remaining = sorted(self.directory.glob("*.json"))
        self.assertFalse(old.exists())
        self.assertEqual(len(remaining), MAX_FILES)
        self.assertFalse((self.directory / f"{0:032x}.json").exists())


if __name__ == "__main__":
    unittest.main()
