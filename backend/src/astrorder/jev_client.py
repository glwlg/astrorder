"""Compatibility proxy forwarding to astrorder.jev.client."""
import sys, importlib
_mod = importlib.import_module('astrorder.jev.client')
sys.modules[__name__] = _mod
