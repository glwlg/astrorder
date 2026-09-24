"""Compatibility proxy forwarding to astrorder.daemon.bridge.hermes_compaction_projection."""
import sys, importlib
sys.modules[__name__] = importlib.import_module("astrorder.daemon.bridge.hermes_compaction_projection")
