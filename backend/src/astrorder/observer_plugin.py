"""Compatibility proxy forwarding to astrorder.observers.plugin."""
import sys, importlib
_mod = importlib.import_module('astrorder.observers.plugin')
sys.modules[__name__] = _mod
