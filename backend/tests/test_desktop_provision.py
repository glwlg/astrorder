from __future__ import annotations

import importlib.util
import json
import shutil
import sys
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parents[2] / "scripts"
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location("provision_desktop", SCRIPTS / "provision_desktop.py")
assert spec is not None and spec.loader is not None
provision_desktop = importlib.util.module_from_spec(spec)
spec.loader.exec_module(provision_desktop)


def test_provision_creates_real_local_configuration(monkeypatch, tmp_path):
    site_packages = tmp_path / "backend/.venv/Lib/site-packages"
    site_packages.mkdir(parents=True)
    monkeypatch.setattr(provision_desktop, "ROOT", tmp_path)
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "local"))
    monkeypatch.setattr(provision_desktop.shutil, "which", lambda _name: None)
    monkeypatch.setattr(provision_desktop, "protect_bytes", lambda value: value)

    provision_desktop.provision()

    config = json.loads((tmp_path / ".runtime/production.json").read_text(encoding="utf-8"))
    credentials = json.loads(
        (tmp_path / ".runtime/production.credentials.dpapi").read_text(encoding="utf-8")
    )
    assert config["environment"]["ASTRORDER_STATIC_DIR"] == str(tmp_path / "frontend/dist")
    assert config["environment"]["ASTRORDER_DATABASE_URL"].endswith(
        "/local/Astrorder/data/astrorder.sqlite3"
    )
    assert all(credentials.values())
    assert (site_packages / "_editable_impl_astrorder_server.pth").read_text() == str(
        tmp_path / "backend/src"
    )

    saved = (tmp_path / ".runtime/production.credentials.dpapi").read_bytes()
    shutil.rmtree(tmp_path / ".runtime")
    provision_desktop.provision()
    assert (tmp_path / ".runtime/production.credentials.dpapi").read_bytes() == saved
