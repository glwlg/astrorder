"""Compatibility proxy forwarding to astrorder.native.commands."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.commands')
sys.modules[__name__] = _mod
