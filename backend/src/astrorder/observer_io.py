"""Compatibility proxy forwarding to astrorder.observers.io."""
import sys, importlib
_mod = importlib.import_module('astrorder.observers.io')
sys.modules[__name__] = _mod
