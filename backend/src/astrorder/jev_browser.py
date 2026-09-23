"""Compatibility proxy forwarding to astrorder.jev.browser."""
import sys, importlib
_mod = importlib.import_module('astrorder.jev.browser')
sys.modules[__name__] = _mod
