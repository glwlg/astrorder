---
name: astrorder-swarm
description: Use when coordinating swarm locks, milestones, telemetry or SOS.
---

# Astrorder Swarm Coordination

Coordination primitives for multi-agent missions: exclusive resource locks, dependency milestones, structured telemetry and SOS escalation. Use the native CLI `astrorder-agent`, never MCP.

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

The runtime derives the calling session; never pass, guess or persist a session id. For locks and SOS, omitting `owner_key`/`key` makes the CLI bind the record to the caller's own session key. Do not supply another session's key as `owner_key` or `key`: the CLI rejects null, empty, non-string and conflicting values before sending anything. Milestones are identified by `milestone_id` and do not carry caller ownership. `target_session_key` remains an explicit notification target and is not rewritten.

## Invocation Pattern

```bash
astrorder-agent invoke <capability> --input-file "<PATH_TO_INPUT.json>"
```

Write each payload as one UTF-8 JSON object. Run `astrorder-agent catalog` to confirm fields.

## Locks (`swarm.lock.*`)

Acquire an exclusive lock before mutating a shared resource:

```json
{"resource": "git:main", "ttl_seconds": 300}
```

```bash
astrorder-agent invoke swarm.lock.acquire --input-file lock.json
astrorder-agent invoke swarm.lock.release --input-file release.json
astrorder-agent invoke swarm.lock.list --input-file empty.json
```

- `resource` is a stable identifier such as `git:main`, `port:3000` or `db:schema`.
- `ttl_seconds` defaults to 300 and is capped at 3600. Release the lock as soon as the critical section ends.
- A failed acquire means another worker owns it; wait or coordinate, never retry in a tight loop.

## Milestones (`swarm.milestone.*`)

Declare a dependency gate before dependent work starts:

```json
{
  "milestone_id": "backend_api_ready",
  "title": "Backend API merged and deployed to staging",
  "wake_session_key": "codex::<child-session>",
  "wake_prompt": "The API gate is resolved; continue the client integration.",
  "metadata": {"contract": "openapi.yaml"}
}
```

```bash
astrorder-agent invoke swarm.milestone.declare --input-file declare.json
astrorder-agent invoke swarm.milestone.resolve --input-file resolve.json
astrorder-agent invoke swarm.milestone.list --input-file empty.json
```

Resolve with `{"milestone_id": "...", "result": {...}}` only when the gate is genuinely satisfied; resolution wakes blocked sessions.

## Telemetry (`swarm.telemetry.*`)

Report progress so the orchestrator does not need to read full transcripts:

```json
{
  "progress": 60,
  "phase": "running_tests",
  "status": "ok",
  "summary": "Unit suite green, integration pending",
  "artifacts": ["app-server-go/internal/capabilities/official_delivery.go"]
}
```

`progress` is 0-100; `status` is `ok`, `warning`, `blocked` or `completed`. Read reports with `swarm.telemetry.get` (`{}` for all workers, or `{"key": "agent_id::session_id"}`).

## SOS (`swarm.sos.*`)

Escalate only a real blocker, conflict or failure:

```json
{
  "reason": "Port 3000 already bound by an unrelated service",
  "context": "listen tcp :3000: bind: address already in use"
}
```

```bash
astrorder-agent invoke swarm.sos.escalate --input-file sos.json
astrorder-agent invoke swarm.sos.list --input-file empty.json
astrorder-agent invoke swarm.sos.resolve --input-file resolve.json
```

Resolve with `{"sos_id": "...", "resolution": "..."}` after the worker is unblocked. Do not use SOS for ordinary questions.

## Rules

- Never hold a lock across an interactive wait; release or let the TTL expire.
- One milestone per externally observable contract, not per file change.
- Report actual states; `completed` requires verified evidence.
