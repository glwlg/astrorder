"""Compatibility proxy forwarding to astrorder.daemon.runtimes.codex.control."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.codex.control")
