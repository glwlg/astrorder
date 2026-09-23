"""Compatibility proxy forwarding to astrorder.native.user_activity."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.user_activity')
sys.modules[__name__] = _mod
