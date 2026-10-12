"""Loader for Astrorder's Hermes connector.

Resolution order: a package copied next to this file (self-contained install, works
for a Hermes desktop started from any directory), then ASTRORDER_PROJECT_ROOT or
the current directory (development checkout).
"""
from __future__ import annotations

import importlib.util
import os
import sys
from pathlib import Path

_HERE = Path(__file__).resolve().parent
_CANDIDATES = [
    _HERE / "astrorder_hermes_plugin" / "__init__.py",
    Path(os.environ.get("ASTRORDER_PROJECT_ROOT", Path.cwd())).resolve()
    / "connectors" / "hermes" / "astrorder_hermes_plugin" / "__init__.py",
]
_SOURCE = next((path for path in _CANDIDATES if path.is_file()), None)
if _SOURCE is None:
    raise RuntimeError("Astrorder Hermes connector source is unavailable")
_SPEC = importlib.util.spec_from_file_location(
    "astrorder_embedded_hermes_connector",
    _SOURCE,
    submodule_search_locations=[str(_SOURCE.parent)],
)
if _SPEC is None or _SPEC.loader is None:
    raise RuntimeError("Astrorder Hermes connector source is unavailable")
_MODULE = importlib.util.module_from_spec(_SPEC)
sys.modules[_SPEC.name] = _MODULE
_SPEC.loader.exec_module(_MODULE)
register = _MODULE.register
