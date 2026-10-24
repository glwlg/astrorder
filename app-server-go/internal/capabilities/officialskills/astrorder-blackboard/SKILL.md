---
name: astrorder-blackboard
description: Use when reading or writing the Astrorder blackboard.
---

# Astrorder Blackboard

The Astrorder blackboard is the shared mission state for multi-agent work: task status, specifications, findings and artifacts. Use the native CLI `astrorder-agent`, never MCP.

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

The runtime derives the calling session; never pass, guess or persist a session id, and never pick identity from the only active session. Missing caller configuration is an error, not a reason to improvise.

## Invocation Pattern

Every call writes one UTF-8 JSON object to a file, then passes that file:

```bash
astrorder-agent invoke blackboard.get --input-file "<PATH_TO_INPUT.json>"
```

Never inline large payloads with `--input`; use `--input-file`. Run `astrorder-agent catalog` to confirm the current fields before constructing a payload.

## Read (`blackboard.get`)

```json
{"key": "build.status"}
```

Omit `key` to list every key in the namespace. Results are returned under `items`.

## Write (`blackboard.set`)

```json
{
  "key": "build.status",
  "value": {
    "component": "StepTimeline",
    "title": "Build pipeline",
    "steps": [
      {"label": "Compile", "state": "done"},
      {"label": "Test", "state": "running"}
    ]
  }
}
```

`value` may be an object, array or string. Declare `component` for Generative UI rendering; otherwise the server may infer one. Prefer `blackboard.auto_render` when the source is raw prose.

## Delete (`blackboard.delete`)

```json
{"key": "build.status"}
```

Use `"key": "*"` only when the user asked to wipe the namespace. Deletion is destructive; confirm the key first.

## Components (`blackboard.components.list`)

```json
{}
```

Lists supported Generative UI component ids and summaries. Read this before choosing a component.

## Component Schema (`blackboard.component.schema`)

```json
{"component_id": "MetricGrid"}
```

Returns the exact JSON schema, required fields and a copyable example for one component. Call it before authoring a non-trivial card instead of guessing field names.

## Auto Render (`blackboard.auto_render`)

```json
{
  "key": "review.summary",
  "title": "Review result",
  "content": "Raw status text, test output or findings to publish."
}
```

The server chooses the best component for the content and publishes it to `key`. Use it for unformatted status reports; use `blackboard.set` when you already know the component and exact shape.

## Rules

- Read before writing when another agent may own the key.
- Keep keys stable and namespaced by mission; do not dump logs into the shared namespace.
- Report the actual CLI result; a written key is not proof that a consumer acted on it.
