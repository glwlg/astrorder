"""Compatibility proxy forwarding to astrorder.daemon.runtimes.ssh.control."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.runtimes.ssh.control")
