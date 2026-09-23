"""Compatibility alias forwarding to astrorder.native.codex."""
import sys
import astrorder.native.codex as _mod
from astrorder.native.codex import *

# Proxy module getattr/setattr so tests monkeypatching astrorder.native_codex mutate the actual module
sys.modules[__name__] = _mod
