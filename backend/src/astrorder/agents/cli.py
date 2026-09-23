from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


def _token() -> str:
    for key in ("ASTRORDER_AGENT_TOKEN", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET"):
        value = os.environ.get(key)
        if value:
            return value
    path = Path(os.environ.get("ASTRORDER_TOKEN_FILE", ".token"))
    if path.is_file():
        return path.read_text(encoding="utf-8").strip()
    raise SystemExit("Missing Astrorder agent token (ASTRORDER_AGENT_TOKEN or .token)")


def _url() -> str:
    return os.environ.get("ASTRORDER_AGENT_URL", "http://127.0.0.1:30001").rstrip("/")


def invoke_remote(capability: str, payload: dict[str, Any]) -> dict[str, Any]:
    body = json.dumps({"capability": capability, "input": payload}, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        f"{_url()}/api/v1/agent/invoke",
        data=body,
        method="POST",
        headers={
            "Authorization": f"Bearer {_token()}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise SystemExit(f"Astrorder agent API {exc.code}: {detail}") from None


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="astrorder-agent", description="Call Astrorder agent APIs")
    sub = parser.add_subparsers(dest="cmd", required=True)
    sub.add_parser("catalog", help="List capabilities")
    invoke_p = sub.add_parser("invoke", help="Call a capability by id")
    invoke_p.add_argument("capability")
    invoke_p.add_argument("--input", default="{}", help="JSON object")
    sessions = sub.add_parser("sessions")
    sessions_sub = sessions.add_subparsers(dest="sessions_cmd", required=True)
    sessions_sub.add_parser("list")
    search = sessions_sub.add_parser("search")
    search.add_argument("q")
    read = sessions_sub.add_parser("read")
    read.add_argument("--key")
    read.add_argument("--agent-id")
    read.add_argument("--session-id")
    read.add_argument("--limit", type=int, default=80)
    sub.add_parser("agents")
    sub.add_parser("machines")
    sub.add_parser("projects")
    args = parser.parse_args(argv)
    if args.cmd == "catalog":
        result = invoke_remote("catalog.list", {})
    elif args.cmd == "invoke":
        payload = json.loads(args.input)
        if not isinstance(payload, dict):
            raise SystemExit("--input must be a JSON object")
        result = invoke_remote(args.capability, payload)
    elif args.cmd == "sessions" and args.sessions_cmd == "list":
        result = invoke_remote("sessions.list", {})
    elif args.cmd == "sessions" and args.sessions_cmd == "search":
        result = invoke_remote("sessions.search", {"q": args.q})
    elif args.cmd == "sessions" and args.sessions_cmd == "read":
        payload: dict[str, Any] = {"limit": args.limit}
        if args.key:
            payload["key"] = args.key
        if args.agent_id:
            payload["agent_id"] = args.agent_id
        if args.session_id:
            payload["session_id"] = args.session_id
        result = invoke_remote("sessions.read", payload)
    elif args.cmd == "agents":
        result = invoke_remote("agents.list", {})
    elif args.cmd == "machines":
        result = invoke_remote("machines.list", {})
    elif args.cmd == "projects":
        result = invoke_remote("projects.list", {})
    else:
        parser.error("unknown command")
        return
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()

