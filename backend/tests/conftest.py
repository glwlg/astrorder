"""Keep default App imports inside disposable test storage, never production."""
import os
from pathlib import Path
import tempfile

# Runs before test module collection (fixtures run too late for main.app).
_root = Path(os.environ.get("LOCALAPPDATA", str(Path.home()))) / "hermes/cache/scratch"
_root.mkdir(parents=True, exist_ok=True)
_test_home = tempfile.TemporaryDirectory(prefix="astrorder-pytest-", dir=_root)
for _name in tuple(os.environ):
    if _name.startswith("ASTRORDER_") and _name not in {
        "ASTRORDER_GO_SESSIOND", "ASTRORDER_REAL_CODEX", "ASTRORDER_REAL_INFERENCE", "ASTRORDER_REAL_SSH_SETTINGS",
    }:
        os.environ.pop(_name)
os.environ.update({
    "ASTRORDER_DATABASE_URL": f"sqlite:///{Path(_test_home.name).as_posix()}/app.db",
    "ASTRORDER_ATTACHMENTS_DIR": str(Path(_test_home.name) / "attachments"),
    "ASTRORDER_AUTO_CONNECT_LOCAL_HERMES": "0",
    "ASTRORDER_SESSION_DAEMON_ENABLED": "0",
    "ASTRORDER_ENABLE_LAUNCH": "0",
})
