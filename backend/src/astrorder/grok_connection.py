"""Compatibility shim for grok_connection, pointing to connectors.grok.connection."""
from connectors.grok.connection import GrokConnection, GrokProjection, local_grok_sessions

__all__ = ["GrokConnection", "GrokProjection", "local_grok_sessions"]
