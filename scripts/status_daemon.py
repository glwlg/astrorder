"""Report verified Session Daemon listener metadata without IPC credentials."""
from __future__ import annotations

import argparse
import json

from daemon_service import ROOT, command_line_for_pid, current_listening_pids, status_payload


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description="Show Astrorder Session Daemon status")
    parser.add_argument("--port", type=int, default=30009)
    args = parser.parse_args(argv)
    print(
        json.dumps(
            status_payload(
                port=args.port,
                pids=current_listening_pids(args.port),
                command_line=command_line_for_pid,
                metadata_path=ROOT / ".runtime" / "session-daemon.json",
            ),
            ensure_ascii=False,
        )
    )


if __name__ == "__main__":
    main()
