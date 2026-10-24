---
name: astrorder-workspace
description: Use when listing Astrorder agents, machines, projects or plugins.
---

# Astrorder Workspace & Plugins

Discovery of the Astrorder instance topology plus UI plugin control. Use the native CLI `astrorder-agent`, never MCP.

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

The runtime derives the calling session; never pass, guess or persist a session id.

## Invocation Pattern

```bash
astrorder-agent invoke <capability> --input-file "<PATH_TO_INPUT.json>"
```

Read-only discovery capabilities take an empty object: write `{}` to a file and pass it.

## Discovery

```bash
astrorder-agent invoke catalog.list --input-file empty.json
astrorder-agent invoke agents.list --input-file empty.json
astrorder-agent invoke machines.list --input-file empty.json
astrorder-agent invoke projects.list --input-file empty.json
```

- `catalog.list` returns every capability id with its input summary. Use it before invoking an unfamiliar capability.
- `agents.list` returns configured agents, including remote SSH agents.
- `machines.list` returns reachable machines (`local` and `ssh-...`) for delegation targets.
- `projects.list` returns projects grouped by instance.

## Plugins (`plugins.*`)

```bash
astrorder-agent invoke plugins.list --input-file empty.json
astrorder-agent invoke plugins.configure --input-file configure.json
astrorder-agent invoke plugins.open --input-file open.json
astrorder-agent invoke plugins.close --input-file close.json
```

Configure or toggle a plugin:

```json
{"plugin_id": "browser", "enabled": true, "config": {}}
```

Open a plugin view or artifact:

```json
{
  "plugin_id": "monaco",
  "path": "app-server-go/internal/capabilities/official_delivery.go",
  "title": "Delivery"
}
```

`plugin_id` is one of `terminal`, `browser`, `gitdiff`, `filetree`, `sidechat`, `agentgraph`, `monaco`, `drawio`, `mermaid`, `excalidraw`, `diff`, `three`, `html`. Pass `path` for file-oriented viewers and `url` for browser/html. Omit `session_key` to target the calling session: the CLI fills it with the caller's own session key. An explicit `session_key` is allowed when the user wants a specific target and the server routes it; explicit null, empty or non-string values are rejected before sending.

Close a tab or collapse the sidecar:

```json
{"plugin_id": "monaco"}
```

```json
{"collapse": true}
```

## Rules

- Discover before assuming an agent id, machine id or plugin id.
- Plugin open/close instructs the UI; it does not prove a human saw the panel.
- Do not enable or reconfigure a plugin the user did not ask about.
