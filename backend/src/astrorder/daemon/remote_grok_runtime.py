"""Compatibility proxy forwarding to astrorder.daemon.runtimes.grok.remote."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.grok.remote")
