"""Installed as __init__.py next to the bundled Hermes connector package."""
from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

_SOURCE = Path(__file__).resolve().parent / "astrorder_hermes_plugin" / "__init__.py"
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
