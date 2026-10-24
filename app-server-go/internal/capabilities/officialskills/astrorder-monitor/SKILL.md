---
name: astrorder-monitor
description: Use when managing the Astrorder Monitor room.
---

# Astrorder Monitor Room

The Monitor room shows several live sessions side by side. Use the native CLI `astrorder-agent`, never MCP.

## Executable Location & Caller Identity

Use this installed skill's verified sibling executable: `<skill_dir>/scripts/astrorder-agent.exe` on Windows or `<skill_dir>/scripts/astrorder-agent` on Unix. Resolve `<skill_dir>` from the Agent's skill loader, never from cwd or an assumed PATH. In commands below, `astrorder-agent` means that exact executable.

Caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`) is resolved by the runtime:
- **Hermes**: Automatically discovers `<home>/astrorder-caller.json` and uses `HERMES_SESSION_ID`.
- **Codex**: Uses `CODEX_THREAD_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Grok**: Uses runtime-provided `GROK_SESSION_ID` and caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`

The runtime derives the calling session; never pass, guess or persist a session id. Monitor targets are explicit session keys, so resolve them from `sessions list` output rather than assuming the newest one.

## Invocation Pattern

```bash
astrorder-agent invoke <capability> --input-file "<PATH_TO_INPUT.json>"
```

## Add Sessions (`monitor.sessions.add`)

```json
{"keys": ["codex::<session-a>", "hermes::<session-b>"]}
```

A single `key` also works. Adding is idempotent for an already visible session.

## Remove Session (`monitor.sessions.remove`)

```json
{"key": "codex::<session-a>"}
```

Removal only affects the dashboard; it never stops or deletes the session.

## Grid Layout (`monitor.layout.set`)

```json
{"columns": 2}
```

`columns` must be 1, 2, 3 or 4.

## Rules

- Use `astrorder-agent sessions list` to obtain real keys; do not invent them.
- Never remove a session the user did not ask about.
- Report the CLI result; a dashboard change is not proof the operator looked at it.
