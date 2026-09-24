"""Compatibility proxy forwarding to astrorder.daemon.runtimes.pty.relay."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.pty.relay")
