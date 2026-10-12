from __future__ import annotations

import hashlib
import importlib.abc
import os
import sqlite3
import sys
import tempfile
import types
import unittest
from contextlib import closing
from pathlib import Path
from unittest.mock import patch

from connectors.hermes.astrorder_hermes_plugin import HermesBridge
from connectors.hermes.astrorder_hermes_plugin.config import HermesConnectorConfig


class RejectAppImports(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname == "astrorder" or fullname.startswith("astrorder."):
            raise AssertionError("Hermes plugin imported the retired Python App")
        return None


class WorkspaceReadTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR"))
        self.addCleanup(self.temporary.cleanup)
        self.home = Path(self.temporary.name)
        constants = types.ModuleType("hermes_constants")
        constants.get_hermes_home = lambda: self.home
        self.constants = patch.dict(sys.modules, {"hermes_constants": constants})
        self.constants.start()
        self.addCleanup(self.constants.stop)
        finder = RejectAppImports()
        sys.meta_path.insert(0, finder)
        self.addCleanup(sys.meta_path.remove, finder)
        self.bridge = HermesBridge(
            None,
            HermesConnectorConfig(
                endpoint="ws://127.0.0.1:1", secret="private-fixture", agent_id="test",
                agent_name="Test", workspace="configured-fallback", connect_on_register=False,
            ),
        )

    def test_workspace_uses_native_host_home_without_python_app(self):
        database = self.home / "state.db"
        with closing(sqlite3.connect(database)) as connection:
            with connection:
                connection.execute("CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT)")
                connection.execute("INSERT INTO sessions VALUES (?, ?)", ("exact-id", "native-workspace"))
        before = hashlib.sha256(database.read_bytes()).digest()
        self.assertEqual(self.bridge._resolve_session_workspace("exact-id"), "native-workspace")
        self.assertEqual(hashlib.sha256(database.read_bytes()).digest(), before)
        self.assertEqual(self.bridge._resolve_session_workspace("missing-id"), "configured-fallback")

    def test_missing_native_database_is_not_created(self):
        self.assertEqual(self.bridge._resolve_session_workspace("exact-id"), "configured-fallback")
        self.assertFalse((self.home / "state.db").exists())


if __name__ == "__main__":
    unittest.main()
