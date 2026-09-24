"""Compatibility proxy forwarding to astrorder.daemon.runtimes.codex.desktop."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.codex.desktop")
