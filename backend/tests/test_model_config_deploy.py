import json
import sys
import tomllib
from pathlib import Path
from types import SimpleNamespace

import pytest

from astrorder.daemon import model_config


def test_local_model_config_is_validated_and_written_atomically(tmp_path, monkeypatch):
    monkeypatch.setattr(Path, "home", classmethod(lambda _cls: tmp_path))
    codex = tmp_path / ".codex/config.toml"
    codex.parent.mkdir()
    codex.write_text('model = "old"\n', encoding="utf-8")

    result = model_config.execute_model_config("model_config.apply", {
        "target": {"kind": "local"},
        "files": {
            "codex_config": 'model = "new"\n',
            "codex_catalog": json.dumps({"models": []}),
            "grok_config": '[models]\ndefault = "grok-4.5"\n',
        },
        "api_key": "",
    })

    assert set(result["changed"]) == {"codex_config", "codex_catalog", "grok_config"}
    assert codex.read_text(encoding="utf-8") == 'model = "new"\n'
    assert list(codex.parent.glob("config.toml.astrorder-backup-*"))

    with pytest.raises(tomllib.TOMLDecodeError):
        model_config.execute_model_config("model_config.apply", {
            "target": {"kind": "local"}, "files": {"codex_config": "[broken"}, "api_key": "",
        })
    assert codex.read_text(encoding="utf-8") == 'model = "new"\n'


def test_local_model_config_rolls_back_empty_file_when_environment_write_fails(tmp_path, monkeypatch):
    monkeypatch.setattr(Path, "home", classmethod(lambda _cls: tmp_path))
    codex = tmp_path / ".codex/config.toml"
    codex.parent.mkdir()
    codex.write_bytes(b"")

    class Key:
        def Close(self):
            pass

    def set_value(_key, _name, _reserved, _kind, value):
        if value == "new-key":
            raise OSError("registry write failed")

    monkeypatch.setitem(sys.modules, "winreg", SimpleNamespace(
        HKEY_CURRENT_USER=object(), REG_SZ=1,
        CreateKey=lambda *_args: Key(), QueryValueEx=lambda *_args: ("old-key", 1),
        SetValueEx=set_value, DeleteValue=lambda *_args: None,
    ))

    with pytest.raises(OSError, match="registry write failed"):
        model_config.execute_model_config("model_config.apply", {
            "target": {"kind": "local"},
            "files": {"codex_config": 'model = "new"\n'},
            "api_key": "new-key",
        })

    assert codex.is_file()
    assert codex.read_bytes() == b""


def test_environment_key_rejects_multiline_values():
    with pytest.raises(model_config.DaemonProtocolError):
        model_config._environment_data("first\nsecond")


def test_missing_hermes_config_is_not_created(tmp_path, monkeypatch):
    monkeypatch.setattr(Path, "home", classmethod(lambda _cls: tmp_path))
    result = model_config.execute_model_config("model_config.apply", {
        "target": {"kind": "local"},
        "files": {
            "codex_config": 'model = "new"\n',
            "hermes_config": "providers:\n  magpie:\n    base_url: http://127.0.0.1:3425/v1\n",
        },
        "api_key": "",
    })
    assert result["changed"] == ["codex_config"]
    assert not (tmp_path / "AppData" / "Local" / "hermes" / "config.yaml").exists()
    planned = model_config.execute_model_config("model_config.plan", {"target": {"kind": "local"}})
    assert planned["hermes_config"]["exists"] is False


def test_existing_hermes_config_is_updated_without_dropping_other_lines(tmp_path, monkeypatch):
    monkeypatch.setattr(Path, "home", classmethod(lambda _cls: tmp_path))
    hermes = tmp_path / "AppData" / "Local" / "hermes" / "config.yaml"
    hermes.parent.mkdir(parents=True)
    hermes.write_text("model:\n  provider: magpie\nkeep: true\n", encoding="utf-8")
    result = model_config.execute_model_config("model_config.apply", {
        "target": {"kind": "local"},
        "files": {"hermes_config": "model:\n  provider: magpie\n  base_url: http://192.168.1.100:3425/v1\nkeep: true\n"},
        "api_key": "",
    })
    assert result["changed"] == ["hermes_config"]
    assert hermes.read_text(encoding="utf-8") == "model:\n  provider: magpie\n  base_url: http://192.168.1.100:3425/v1\nkeep: true\n"


def test_gateway_keys_are_written_to_separate_environment_names(tmp_path, monkeypatch):
    monkeypatch.setattr(Path, "home", classmethod(lambda _cls: tmp_path))
    written = {}

    class Key:
        def Close(self):
            pass

    def query_value(*_args):
        raise FileNotFoundError

    monkeypatch.setitem(sys.modules, "winreg", SimpleNamespace(
        HKEY_CURRENT_USER=object(), REG_SZ=1,
        CreateKey=lambda *_args: Key(), QueryValueEx=query_value,
        SetValueEx=lambda _key, name, _reserved, _kind, value: written.__setitem__(name, value),
        DeleteValue=lambda *_args: None,
    ))
    model_config.execute_model_config("model_config.apply", {
        "target": {"kind": "local"},
        "files": {"codex_config": 'model = "new"\n'},
        "api_key": "ocx-key",
        "api_keys": {"OPENCODEX_API_AUTH_TOKEN": "ocx-key", "MAGPIE_API_KEY": "magpie-key", "OTHER": "ignored"},
    })
    assert written == {"OPENCODEX_API_AUTH_TOKEN": "ocx-key", "MAGPIE_API_KEY": "magpie-key"}
