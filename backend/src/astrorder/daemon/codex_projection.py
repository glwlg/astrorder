"""Compatibility proxy forwarding to astrorder.daemon.bridge.codex_projection."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.bridge.codex_projection")
