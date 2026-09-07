# Integration report — 星序 · Astrorder

## Status

The local HTTP/WS/SPA vertical slice is verified with a real owned FastAPI process, a built frontend, Chromium desktop/mobile Playwright, and an inert connector. This is not a claim that Hermes or Codex native runtime integration is complete: no plugin was installed or enabled, no native session received a prompt, and no live Agent transport was exercised.

## Scope and isolation

- Read `docs/PRODUCT.md`, `docs/CONTRACT.md`, `docs/reports/frontend.md`, and `docs/reports/backend.md` before changes.
- Confirmed the prior frontend/backend development processes were not serving Vite or FastAPI before the integration run. The `.runtime/orchestration.json` status was treated as handoff data, not acceptance evidence.
- Browser verification used an owned FastAPI server at `127.0.0.1:18765`, private temporary SQLite/attachment paths under `.runtime/terra-e2e-evidence/`, distinct test-only browser/connector credentials, and `scripts/inert_connector.py`.
- The built frontend was served by FastAPI from `frontend/dist`; Vite was not used for the passing browser run.
- The owned FastAPI and inert-connector processes were terminated after evidence collection. The health endpoint no longer responded on port 18765.
- No installed Hermes/Codex files, credential files, Desktop/Overlook process, relay, existing session, plugin profile, or remote deployment was modified.

## Integration fixes

- Added SPA fallback for direct `/chat`, `/monitor`, and `/agents` routes while preserving a 404 for unknown `/api/...` routes.
- Kept command receipts scoped by `agent_id + session_id + command_id`; same command IDs in distinct scopes no longer overwrite one another.
- Scoped frontend event replay deduplication by connector agent plus event ID, matching server source-event behavior.
- Hydrated durable command receipts after reload/reconnect instead of only retaining locally created optimistic outbox entries.
- Reconciled paged history by stable message IDs: latest tail updates in place and only unseen older records prepend. Refetching a tail no longer moves old history after new messages.
- Changed ambiguous connector-write behavior to persist and return `unknown`, not label it failed. The service records a command as sent before the write so a write/disconnect ambiguity cannot remain incorrectly `received`.
- Reworked Hermes/Codex connector transports so server-to-connector frames cannot cancel and lose queued outbound events.
- Kept managed Hermes/Codex launch visibly unavailable. A detached `codex app-server` process is not represented as a connected Agent.
- Changed manual “back to bottom” scroll to deterministic programmatic scrolling so smooth-scroll events do not impersonate user browsing on mobile.
- Reconciled `Session.workspace` as `string|null`, added `Command.target_id` to the wire shape, documented connector `pull`, and documented unknown-command responses in `docs/CONTRACT.md`.

## Verified

### Automated checks

| Command | Result |
|---|---|
| `backend: uv run pytest -q` | exit 0 — 29 passed, 2 upstream TestClient/AnyIO deprecation warnings |
| `backend: uv run ruff check .` | exit 0 — all checks passed |
| `backend: uv run ruff check ../connectors --output-format=concise` | exit 0 — all checks passed |
| `backend: uv run ruff check ../scripts --output-format=concise` | exit 0 — all checks passed |
| `connectors/hermes: uv build` | exit 0 — source distribution and wheel built |
| `connectors/codex: uv build` | exit 0 — source distribution and wheel built |
| `frontend: npm run test` | exit 0 — 9 files, 27 tests passed |
| `frontend: npm run lint` | exit 0 |
| `frontend: npm run build` | exit 0 — TypeScript and Vite build completed |
| `root: python scripts/test_orchestrate.py` | exit 0 — 4 tests passed |

### Real FastAPI and browser evidence

`frontend/e2e/astrorder.spec.ts` ran against the built SPA and real loopback FastAPI with the test-only inert connector:

    ASTRORDER_E2E_BASE_URL=http://127.0.0.1:18765 \
    ASTRORDER_E2E_TOKEN=<test-only-browser-token> \
    ASTRORDER_E2E_EXECUTABLE=<local-Chromium-path> \
    npm run test:e2e

Exit 0: 12 Playwright tests passed across Chromium desktop and mobile projects.

The exact scenarios cover:

- unauthenticated auth screen and no fabricated private UI;
- direct built-SPA session route, login cookie, chat/monitor shared state;
- two identical legitimate sends remaining two canonical replies and two durable receipts after page reload/replay;
- image-only upload, authenticated image rendering, and replay without duplication;
- connector disconnect becoming one explicit `unknown` receipt with no automatic resend or duplicate on reload;
- sticky-follow, genuine upward browse pause, deterministic manual return to bottom, disabled unsupported controls, and no horizontal overflow;
- visible failed command and managed-runtime disabled state.

Artifacts:

- `P:/workspace/glwlg/ai/astrorder/.runtime/terra-e2e-evidence/fastapi.log`
- `P:/workspace/glwlg/ai/astrorder/.runtime/terra-e2e-evidence/security-gates.json`
- `P:/workspace/glwlg/ai/astrorder/frontend/test-results/astrorder-real-isolated-Fa-e5077-ontrols-and-stays-in-bounds-desktop/isolated-chat-desktop.png`
- `P:/workspace/glwlg/ai/astrorder/frontend/test-results/astrorder-real-isolated-Fa-e5077-ontrols-and-stays-in-bounds-mobile/isolated-chat-mobile.png`

The visual artifacts show the isolated session and outbox states at both viewports. Playwright DOM measurements asserted `scrollWidth <= clientWidth`; this is not a visual-only conclusion.

### Security gates on the real isolated server

`scripts/verify_isolated_security.py` exited 0 and wrote `security-gates.json`. It verified:

- versioned public `/health` and unauthenticated private bootstrap rejection;
- rejected evil Origin during login and authenticated same-origin login;
- direct SPA route served from FastAPI and unknown API route stayed 404;
- upload filename traversal rejection;
- shell-shaped workspace input rejected at managed runtime launch;
- unauthenticated browser WebSocket, browser/connector credential role escalation, and evil-Origin connector WebSocket rejection.

The server log records the rejected WebSocket handshakes as HTTP 403. Attachment containment against a tampered metadata path is also covered by backend tests.

### Native surface evidence, not live proof

Read-only verification found Hermes Agent `v0.21.0` and Codex CLI `0.150.0`. `hermes plugins --help` exposes install/enable/doctor commands but none was invoked. Local Hermes source confirms `PluginContext.inject_message(content, role, session_key)` only reports async acceptance, and confirms public session/stream hook names. Official Hermes plugin documentation confirms `register(ctx)`, hooks, entry-point distribution, and gateway-injection authorization.

`codex app-server --help` and the official app-server documentation confirm app-server JSON-RPC with loopback stdio transport, `initialize`/`initialized`, thread/turn methods, streamed item notifications, and interrupt support. Official Codex hooks documentation describes observer/script hooks, not a persistent bidirectional Astrorder transport.

The connector artifacts therefore have source/API support for their claimed protocol boundaries, but fixture IDs and fake app-server tests do not prove native message/command persistence.

## Failed or nonzero observations

- No final required check failed.
- `pytest` still emits two upstream deprecation warnings from the installed Starlette/httpx/AnyIO TestClient stack.
- Vitest emitted Node `--localstorage-file` path warnings from the local runner, while all 27 tests passed.
- Vite reports one non-fatal current single-bundle chunk-size warning.
- During an earlier browser reconnect, Uvicorn logged one Windows Proactor `WinError 10054` socket-reset callback. The application continued, the final isolated run passed all 12 tests, and no application traceback or test failure followed. It remains an environment-level warning worth rechecking after a Uvicorn/asyncio upgrade.

## Unsupported and unverified

- Native Hermes and Codex lifecycle runs remain unverified. The inert connector proves Astrorder protocol behavior only.
- Hermes bridge reports only `chat` and `events`; it does not claim attachments, queue, stop, approvals, history, native completion, or browser-command ID persistence.
- Codex companion reports only `chat`, `stop`, and `events`; attachment mapping, queue, approvals, history, and durable browser-command correlation remain unsupported.
- Managed runtime launch intentionally remains unavailable for both kinds. A raw CLI/app-server process does not create a connected Astrorder Agent.

## User action required

1. Configure distinct browser and connector secrets, exact allowed loopback origins, a durable SQLite/attachment location, and workspace allowlists for any real deployment.
2. Explicitly authorize installation and enabling of the Hermes plugin only in an isolated profile/workspace, then authorize an isolated native lifecycle smoke test.
3. Explicitly authorize a Codex companion run against an isolated thread/workspace if native app-server behavior is to be verified.
4. For either native smoke test, collect real connector hello/session/stream/stop/disconnect evidence without sending prompts to an existing user session.

## Approval-gated work skipped

- Plugin installation/enabling, live Desktop/relay restart, native runtime launch against a real Agent, credential-file access, and prompt submission to existing sessions were not attempted.
- A Python-based process-command inspection was blocked by the approval policy; tasklist/netstat and loopback health checks were used instead.
- Playwright browser installation was not attempted. A pre-existing local Chromium executable was used for the isolated browser run.
