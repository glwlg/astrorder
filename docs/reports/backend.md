# Backend report — 星序 · Astrorder

Status: backend protocol/service implementation complete; native live-agent verification is not claimed.

Ownership respected: changes are under `backend/`, `connectors/`, and this report. `docs/PRODUCT.md` and frozen `docs/CONTRACT.md` were read from the absolute repository paths before implementation. `docs/CONTRACT.md`, frontend files, Hermes-plugins, installed Hermes source, and installed Codex runtime files were not modified. No commit, push, Desktop/Overlook restart, prompt submission, or credential-file read was performed.

## Implemented

- Independent FastAPI application factory with loopback defaults (`127.0.0.1:8765`), public `/health`, safe exception responses, explicit environment/constructor settings, and fail-closed private authentication.
- Separate browser cookie/Bearer authentication and connector Bearer role. Mutating HTTP routes and browser WebSocket routes validate configured Origin values.
- SQLAlchemy/SQLite durable agents, scoped sessions, canonical messages, commands/outbox, attachments, and durable cursor events. The transcript and outbox use different tables and serializers.
- HTTP bootstrap, agents, sessions, history paging, command/outbox, auth, attachment, and runtime endpoints. Browser command submission exists only on HTTP.
- Durable command idempotency by scoped `(agent_id, session_id, id)`, payload mismatch `409`, preserved failed attempts, explicit queue acceptance, accepted/running/completed/failed/unknown/cancelled transitions, and no resend after ambiguous submission.
- Connector WebSocket hello/identity validation, source-event dedupe, server cursor assignment, command confirmations, multi-client event fan-out, bounded replay, cursor reset events, and shutdown/disconnect handling.
- Attachment filename/path/content-type/size checks, private same-origin download route, generated storage names, and resolved-path containment.
- Static frontend mounting only when a configured or repository `frontend/dist/index.html` exists.
- Fixed-argument, `shell=False` process supervision with workspace allowlists. Runtime launch is intentionally separate from the chat connector; Hermes managed launch reports unsupported until a verified headless transport exists.
- Installable Hermes plugin and Codex app-server companion artifacts under `connectors/`, with source-compatible inert tests and explicit limitations. Details and source evidence are in `connectors/README.md`.

## Verification commands and results

All commands below were run against the actual repository. Exit code is stated from the tool result.

From `P:/workspace/glwlg/ai/astrorder/backend`:

```text
uv run pytest -q
```

Exit code 0. Result: `24 passed, 2 warnings`. The warnings are existing Starlette/httpx and AnyIO deprecation warnings from the installed TestClient stack; no test failed.

```text
uv run ruff check .
```

Exit code 0. Result: `All checks passed!`

Additional connector lint:

```text
uv run ruff check ../connectors --output-format=concise
```

Exit code 0. Result: `All checks passed!`

Artifact builds:

```text
cd P:/workspace/glwlg/ai/astrorder/connectors/hermes
uv build
```

Exit code 0. Result: built `dist/astrorder_hermes_connector-0.1.0.tar.gz` and `dist/astrorder_hermes_connector-0.1.0-py3-none-any.whl`.

```text
cd P:/workspace/glwlg/ai/astrorder/connectors/codex
uv build
```

Exit code 0. Result: built `dist/astrorder_codex_connector-0.1.0.tar.gz` and `dist/astrorder_codex_connector-0.1.0-py3-none-any.whl`.

Isolated server smoke test:

```text
cd P:/workspace/glwlg/ai/astrorder/backend
ASTRORDER_PORT=18765 uv run astrorder-server
curl.exe --fail --silent --show-error http://127.0.0.1:18765/health
```

The server was started as an explicitly owned background process, and the curl request exited 0 with:

```json
{"status":"ok","service":"astrorder","protocol_version":1}
```

The process log showed application startup and the `GET /health` 200 response. The process manager then killed that owned smoke process; no Desktop/Overlook process was touched.

## Native surface evidence

The installed executables were inspected without starting a conversation:

```text
hermes --version
```

Exit code 0: Hermes Agent v0.21.0 (2026.8.31), upstream `245e4800`, install directory `C:\Users\luwei\AppData\Local\hermes\hermes-agent`.

```text
codex --version
```

Exit code 0: `codex-cli 0.150.0`.

```text
hermes plugins --help
hermes plugins list --help
codex plugin --help
codex app-server --help
codex app-server generate-json-schema --help
```

Each exited 0. The local Hermes CLI exposes plugin install/enable/list/doctor commands. The installed Codex CLI exposes a `plugin` marketplace/config command and an experimental `app-server`; its help lists `--listen stdio://`/`--stdio`, websocket/unix listener forms, and JSON schema generation. `codex hooks --help` and plural `codex plugins --help` fell back to the general CLI help rather than exposing a persistent hook/connector registration command.

Read-only Hermes source evidence used by the connector implementation:

- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:1-8` — directory plugin `register(ctx)`, valid hook registration, and `hermes_agent.plugins` entry points.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:107-125` — installed `VALID_HOOKS`, including lifecycle and stream hooks.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:411-418` — public `PluginContext.spawn_task`.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:596-602` — `ctx.inject_message(...) -> bool` and its async-acceptance meaning.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:893-895` — `ctx.register_hook(...)`.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\agent\plugin_stream_hooks.py:124-136` — bounded queued stream observers.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\agent\stream_delivery.py:269-315` — `session_id`, `turn_id`, stream lifecycle, delta, and final payload fields.

Official documentation evidence:

- Hermes plugin guide: https://hermes-agent.nousresearch.com/docs/developer-guide/plugins
- Hermes plugin/injection guide: https://hermes-agent.nousresearch.com/docs/user-guide/features/plugins
- Codex app-server: https://developers.openai.com/codex/app-server (currently redirects to ChatGPT Learn content titled `Codex App Server`; it documents JSON-RPC over stdio JSONL, `initialize`/`initialized`, `thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`, `item/agentMessage/delta`, and `turn/completed`).
- Codex hooks: https://developers.openai.com/codex/hooks (script hooks such as `UserPromptSubmit`, `Stop`, `SessionStart`, and `SessionEnd`, with trust review; no persistent bidirectional transport contract).
- Codex plugin build guide: https://developers.openai.com/codex/plugins/build (required `.codex-plugin/plugin.json` and bundled components).

No real Hermes or Codex prompt was submitted. The native evidence proves the extension/API surfaces and executable versions, not a live end-to-end agent run.

## Additive integration notes

These are proposed additive details for the later integration agent; the frozen contract was not changed.

1. `Command` responses retain optional `target_id` so stop/approve/cancel outbox entries can be rendered without a second object shape.
2. Connector source-event dedupe is scoped by `(agent_id, event.id)` because the contract defines IDs as agent/session scoped. The durable cursor remains server-assigned.
3. Connector clients may send an additive `{"type":"pull"}` frame to request queued commands after an explicit reconnect. `unknown` commands are never pulled or resent; only intentional `enqueue` commands remain queued.
4. `send` remains `received` until a connector emits `command.upsert`. A frame write followed by disconnect becomes `unknown`; it is not native turn completion. `enqueue` is `queued` acceptance and not a completion claim.
5. Browser event WebSocket frames are raw `Event` objects. Connector command/event frames are role-specific envelopes. `resync_required.data.reason` is additive diagnostic data; the client refetches bootstrap, transcript, and outbox.
6. Hermes text injection and Codex app-server turn requests cannot prove persistence of the browser command ID inside the native runtime. Connector-emitted canonical messages therefore use `command_id: null` unless a future documented native correlation field is verified.
7. Hermes reports only `chat` and `events`; Codex reports only `chat`, `stop`, and `events`. Unsupported capabilities carry a limitation rather than a simulated adapter.

## Setup needed and remaining blockers

- Set distinct browser and connector secrets in the process environment, configure exact allowed Origins, a durable SQLite path, attachment directory, and workspace roots. Never put populated values in the example files.
- Install the Hermes wheel/package into the intended Hermes environment and explicitly enable the plugin; supply an active Hermes lifecycle session. This was not done automatically.
- Install the Codex companion wheel and run it with a separately configured allowlisted workspace. For an existing Codex thread, supply its explicit app-server thread ID; otherwise the companion starts a new thread through `thread/start`.
- A live native smoke test still needs an isolated Hermes session and Codex app-server workspace plus explicit operator authorization. It must be non-destructive and must record real connector hello, session event, one safe command, stream, cancel/stop, disconnect, and durable unknown-state results. It was not run here, so native integration remains unverified.
- Hermes public injection cannot report completion, attachments, stop, approvals, or history with the inspected APIs. Codex app-server attachment URL mapping, approvals, queue, and history are intentionally not implemented. These remain visible unsupported capabilities, not fake fallbacks.
- The current TestClient run emits two upstream deprecation warnings; they do not block the required passing pytest/ruff gates.
