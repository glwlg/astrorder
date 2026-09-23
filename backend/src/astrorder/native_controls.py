"""Compatibility proxy forwarding to astrorder.native.controls."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.controls')
sys.modules[__name__] = _mod
