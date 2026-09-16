"""Shared daemon protocol errors."""


class DaemonProtocolError(ValueError):
    """A daemon caller supplied an invalid local IPC value."""