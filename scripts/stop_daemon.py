"""Stop exactly one verified Astrorder Session Daemon listener."""
from __future__ import annotations

import argparse
import os

from daemon_service import stop_daemon


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description="Stop Astrorder Session Daemon")
    parser.add_argument("--port", type=int, default=30009)
    args = parser.parse_args(argv)
    pid = stop_daemon(
        port=args.port,
        secret=os.environ.get("ASTRORDER_SESSION_DAEMON_SECRET") or None,
    )
    print("Session Daemon was not listening." if pid is None else f"Stopped Session Daemon PID: {pid}")


if __name__ == "__main__":
    main()
