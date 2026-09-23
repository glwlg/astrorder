"""Compatibility proxy forwarding to astrorder.native.identity_migration."""
import sys, importlib
_mod = importlib.import_module('astrorder.native.identity_migration')
sys.modules[__name__] = _mod
