"""Project-local loader for Astrorder's separately packaged Hermes connector."""
from __future__ import annotations

import importlib.util
import os
import sys
from pathlib import Path

_PROJECT_ROOT = Path(os.environ.get("ASTRORDER_PROJECT_ROOT", Path.cwd())).resolve()
_SOURCE = (
    _PROJECT_ROOT
    / "connectors"
    / "hermes"
    / "astrorder_hermes_plugin"
    / "__init__.py"
)
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
