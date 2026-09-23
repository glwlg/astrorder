"""Compatibility proxy forwarding to astrorder.native.history_page."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.history_page')
sys.modules[__name__] = _mod
