"""Compatibility proxy forwarding to astrorder.native.observers."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.observers')
sys.modules[__name__] = _mod
