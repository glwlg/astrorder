"""Start the independently managed Astrorder Session Daemon."""
from __future__ import annotations

import argparse

from daemon_service import start_daemon


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description="Start Astrorder Session Daemon")
    parser.add_argument("--port", type=int, default=30009)
    parser.add_argument("--enable-codex", action="store_true")
    parser.add_argument("--codex-executable")
    parser.add_argument("--codex-workspace")
    parser.add_argument("--codex-allowed-workspace", action="append", default=[])
    parser.add_argument("--codex-agent-id", default="daemon-codex")
    parser.add_argument("--codex-agent-name", default="Daemon Codex")
    parser.add_argument("--enable-pty", action="store_true")
    parser.add_argument("--pty-allowed-workspace", action="append", default=[])
    parser.add_argument("--enable-hermes", action="store_true")
    parser.add_argument("--enable-ssh", action="store_true")
    args = parser.parse_args(argv)
    runtime_args: list[str] = []
    if args.enable_codex:
        if not args.codex_executable or not args.codex_workspace:
            parser.error("--codex-executable and --codex-workspace are required with --enable-codex")
        runtime_args.extend(
            [
                "--enable-codex",
                "--codex-executable",
                args.codex_executable,
                "--codex-workspace",
                args.codex_workspace,
                "--codex-agent-id",
                args.codex_agent_id,
                "--codex-agent-name",
                args.codex_agent_name,
            ]
        )
        for workspace in args.codex_allowed_workspace:
            runtime_args.extend(["--codex-allowed-workspace", workspace])
    if args.enable_pty:
        if not args.pty_allowed_workspace:
            parser.error("--pty-allowed-workspace is required with --enable-pty")
        runtime_args.append("--enable-pty")
        for workspace in args.pty_allowed_workspace:
            runtime_args.extend(["--pty-allowed-workspace", workspace])
    if args.enable_hermes:
        runtime_args.append("--enable-hermes")
    if args.enable_ssh:
        runtime_args.append("--enable-ssh")
    print(f"Session Daemon PID: {start_daemon(port=args.port, runtime_args=runtime_args)}")


if __name__ == "__main__":
    main()
