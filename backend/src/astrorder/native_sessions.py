"""Compatibility proxy forwarding to astrorder.native.sessions."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.sessions')
sys.modules[__name__] = _mod
