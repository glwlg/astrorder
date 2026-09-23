"""Compatibility proxy forwarding to astrorder.native.attachments."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.attachments')
sys.modules[__name__] = _mod
