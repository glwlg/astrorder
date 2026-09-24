"""Compatibility proxy forwarding to astrorder.daemon.runtimes.ssh.runtime."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.ssh.runtime")
