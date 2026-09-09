# Astrorder backend

This directory contains the independent FastAPI control server. It owns the durable SQLite state and the browser/connector protocol; it does not depend on Hermes Desktop or the Overlook relay.

## Run locally

From `backend/`:

```text
uv sync --extra dev
uv run uvicorn astrorder.main:app --host 127.0.0.1 --port 30002
```

The default bind is loopback (`127.0.0.1:30002`). `/health` is public. All state, attachment, runtime, and WebSocket routes fail closed until `ASTRORDER_BROWSER_SECRET` (or the explicitly supported alias `ASTRORDER_SECRET`) is set. The connector WebSocket requires a different `ASTRORDER_CONNECTOR_SECRET`.

Settings are read from explicit constructor values in tests or `ASTRORDER_*` environment variables; no dotenv or credential files are loaded. Native adapters use documented runtime protocols and bounded, read-only session database queries. Model metadata queries select only model/provider/branch fields, not credentials or full transcripts.

## Configuration example

Copy the placeholders into the process environment using the platform's secret store; do not commit a populated environment file.

```text
ASTRORDER_HOST=127.0.0.1
ASTRORDER_PORT=30002
ASTRORDER_DATABASE_URL=sqlite:///./data/astrorder.sqlite3
ASTRORDER_ATTACHMENTS_DIR=./data/attachments
ASTRORDER_STATIC_DIR=../frontend/dist
ASTRORDER_ALLOWED_ORIGINS=http://127.0.0.1:30001,http://localhost:30001,http://127.0.0.1:30002,http://localhost:30002
ASTRORDER_BROWSER_SECRET=<generate-a-long-random-value>
ASTRORDER_CONNECTOR_SECRET=<generate-a-different-long-random-value>
ASTRORDER_MAX_ATTACHMENT_SIZE=10485760
ASTRORDER_EVENT_RETENTION=1000
ASTRORDER_ALLOWED_WORKSPACES=<absolute-workspace-root>
ASTRORDER_ENABLE_LAUNCH=false
ASTRORDER_CODEX_EXECUTABLE=<absolute-codex-executable-optional>
ASTRORDER_HERMES_EXECUTABLE=<absolute-hermes-executable-optional>
ASTRORDER_AUTO_CONNECT_LOCAL_HERMES=false
```

`ASTRORDER_ALLOWED_WORKSPACES` is a comma-separated list of existing roots. Runtime launch accepts only `hermes` or `codex`, constructs fixed arguments, uses `shell=False`, and supervises only processes it started. A launch is not a connector or chat transport. Local Hermes connection is separate: the authenticated `/api/v1/connections/local/connect` route discovers Hermes, installs only the checked Astrorder wrapper into the active profile, and starts an owned native TUI-gateway runtime. `ASTRORDER_AUTO_CONNECT_LOCAL_HERMES=true` is reserved for an explicitly operated local preview, never the default.

## Data and API semantics

### Local Codex

The editable Codex connector dependency is declared in `pyproject.toml` and installed by `uv sync`. Use the desktop connection manager or the mobile header's Codex connection button. The authenticated `/api/v1/connections/codex/connect` endpoint initializes an owned native app-server and discovers existing threads without sending a prompt. Disconnect closes only this transport, not other Codex processes. This is separate from the legacy managed-launch endpoint.

`tests/codex_readonly_smoke.py` and `frontend/e2e/codex-native-readonly.mjs` are explicit manual native probes, not ordinary isolated unit tests. They use temporary Astrorder caches, read native metadata/messages without printing content, and close their owned transport. Do not run native prompt, model-change, or deletion tests against existing user threads.

- Commands are HTTP-only browser submissions. The browser-generated command ID and full payload are durable; a reused ID with a different payload returns `409`.
- Commands are the outbox. Canonical transcript messages are stored separately and arrive from connector events. Equal text, timestamps, or ordering never merge IDs.
- A `send` remains `received` until the connector confirms with `command.upsert`; if its frame was already written and the connector disconnects, it changes to `unknown` and is not resent automatically. `enqueue` is durable `queued` acceptance and is not a completion claim.
- Connector events carry a source event ID; the server validates agent/session identity, assigns the durable cursor, and ignores duplicate source IDs in that agent scope.
- Browser event WebSockets replay durable events after an opaque cursor. A cursor outside the bounded retention window yields `resync_required`; the client must refetch bootstrap, transcript, and outbox.
- Attachment URLs are authenticated same-origin routes. Uploaded names, media types, size, and storage paths are bounded and validated; the API never accepts a filesystem path for download.

## Tests

```text
uv run pytest
uv run ruff check .
```

The connector artifact tests are run with the same backend test command. Native-agent verification is intentionally separate from inert protocol tests.
