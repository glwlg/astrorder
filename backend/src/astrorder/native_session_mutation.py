"""Compatibility proxy forwarding to astrorder.native.session_mutation."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.session_mutation')
sys.modules[__name__] = _mod
