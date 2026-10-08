"""Start the independently managed Astrorder Session Daemon."""
from __future__ import annotations

import argparse

from daemon_service import start_daemon


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description="Start Astrorder Session Daemon")
    parser.add_argument("--port", type=int, default=30009)
    args = parser.parse_args(argv)
    print(f"Session Daemon PID: {start_daemon(port=args.port)}")


if __name__ == "__main__":
    main()
