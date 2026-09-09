# Astrorder embedded connectors

These are connector artifacts for the Astrorder `/ws/v1/connector` role. They are not Desktop plugins, renderer bridges, stdout text scrapers, generic CLI chat wrappers, or ACP-only proxies. The authenticated local-Hermes flow may install only the checked Astrorder wrapper into the active Hermes profile after an explicit operator action; it never alters Hermes core, another profile, Desktop, gateway, Overlook, or Codex.

The connector secret is intentionally separate from the browser secret. Both artifacts read only explicit `ASTRORDER_*` environment variables. They do not read Hermes/Codex credential files and do not contain secrets.

## Hermes artifact

Path: `connectors/hermes/`

Install either the wheel in `dist/` or the source project into the Hermes Python environment:

```text
uv pip install ./connectors/hermes
```

The package exposes the documented `hermes_agent.plugins` entry-point group and `astrorder_hermes_plugin:register`. The local UI uses a tiny wrapper in `.hermes/plugins/astrorder-hermes` that dynamically loads this package from the project root. On an explicit local connection, the server copies only that checked wrapper into the active Hermes profile's `plugins/astrorder-hermes` directory and invokes `hermes plugins enable astrorder-hermes`; a mismatching pre-existing plugin is rejected rather than overwritten.

Required process environment variables are `ASTRORDER_CONNECTOR_SECRET` and `ASTRORDER_HERMES_AGENT_ID`. Optional values are `ASTRORDER_CONNECTOR_ENDPOINT`, `ASTRORDER_HERMES_AGENT_NAME`, `ASTRORDER_HERMES_WORKSPACE`, `ASTRORDER_PROJECT_ROOT`, `ASTRORDER_HERMES_CONNECT_ON_REGISTER`, and `ASTRORDER_HERMES_SESSION_KEY`. `ASTRORDER_HERMES_SESSION_KEY` is only useful for an existing authorized gateway route; it is not a new route or a credential.

Advertised capabilities are exactly `chat` and `events`:

- `chat`: Astrorder-owned local TUI sessions are sent by the control server to documented TUI-gateway `prompt.submit`, using the server-private live ID mapped from the exact durable agent/session identity. A successful JSON-RPC result becomes `accepted`, never fabricated `completed`. Other Hermes host modes retain documented `ctx.inject_message(content, role="user", session_key=...)` behavior and require that host's existing authorized route.
- `events`: `on_session_start`, `on_session_end`, `on_session_finalize`, `post_llm_call`, and stream observer hooks produce session and canonical `message.upsert` events. Stream updates use the documented `turn_id` as the stable coalescing identity.
- `attachments`, `queue`, `stop`, `approvals`, and `history` are not advertised. The plugin API has no proof that these operations can be carried out through this bridge.
- Observed canonical messages set `command_id` to `null`. Hermes `ctx.inject_message` has no documented client-command ID parameter and a passed browser ID is not claimed to be persisted by Hermes.

The transport starts during plugin registration when the explicit connector environment is present, then owns a bidirectional WebSocket thread independent of Desktop. Session lifecycle hooks report only actual sessions/messages. It performs one connection attempt and never replays a command after disconnect. A disconnect therefore remains visible to the server as `unknown` when appropriate.

## Codex artifact

Path: `connectors/codex/`

The companion is installable and has a fixed entry point:

```text
uv pip install ./connectors/codex
astrorder-codex-connector
```

It requires `ASTRORDER_CONNECTOR_SECRET`, `ASTRORDER_CODEX_AGENT_ID`, `ASTRORDER_CODEX_WORKSPACE`, and `ASTRORDER_ALLOWED_WORKSPACES`. Optional values are `ASTRORDER_CONNECTOR_ENDPOINT`, `ASTRORDER_CODEX_AGENT_NAME`, `ASTRORDER_CODEX_EXECUTABLE`, and `ASTRORDER_CODEX_THREAD_ID`. The companion constructs only `[codex, app-server, --listen, stdio://]`, uses `shell=False`, and rejects workspaces outside the explicit allowlist.

Advertised capabilities are exactly `chat`, `stop`, and `events`:

- `chat`: `send` becomes the documented `turn/start` request with a text input on the attached/start thread.
- `stop`: `stop`/`cancel` uses the documented `turn/interrupt` method with an explicit target turn ID.
- `events`: `item/agentMessage/delta` and `turn/completed` JSON-RPC notifications are converted into canonical message/command events using thread/turn identities.
- `attachments`, `queue`, `approvals`, and `history` are not advertised. The current artifact does not map authenticated Astrorder attachment URLs to Codex local-image inputs, and it does not claim an approval or history API that was not exercised.
- Codex app-server does not receive Astrorder's browser command ID in `turn/start`. The companion keeps an in-memory correlation only for the live turn and emits canonical observed messages with `command_id: null`; it never claims Codex persisted the browser ID.

The companion speaks the app-server JSONL protocol over the child process's stdio. That is structured JSON-RPC transport, not scraping terminal prose. `codex app-server` is the runtime integration; the packaged `.codex-plugin/plugin.json` is setup guidance only. The documented Codex hook/plugin lifecycle is observer/configuration oriented and is not used as a fake bidirectional transport.

## Source and official API evidence

Hermes local source was inspected read-only:

- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:1-8` states that directory plugins expose `register(ctx)`, register hooks, and can be distributed through the `hermes_agent.plugins` entry-point group.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:107-125` defines the installed `VALID_HOOKS`, including `on_stream_start`, `on_stream_delta`, `on_stream_end`, `on_session_start`, `on_session_end`, and `on_session_finalize`.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:411-418` defines the supervised `PluginContext.spawn_task` surface; this connector instead uses a small owned thread because registration itself is not assumed to run inside a loop.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:596-602` defines `ctx.inject_message(content, role="user", *, session_key=None) -> bool` and explicitly says `True` is acceptance for async dispatch, not delivery completion.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\hermes_cli\plugins.py:893-895` defines `ctx.register_hook(hook_name, callback)`.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\agent\plugin_stream_hooks.py:124-136` shows stream callbacks receive queued payloads on bounded observer queues.
- `C:\Users\luwei\AppData\Local\hermes\hermes-agent\agent\stream_delivery.py:269-315` shows the exact stream payload fields: `session_id`, `turn_id`, `model`, `provider`, `surface`, `on_stream_start`, `on_stream_delta(delta, kind)`, and `on_stream_end(final_text, finished, error)`.
- The installed Hermes docs at `C:\Users\luwei\AppData\Local\hermes\hermes-agent\website\docs\user-guide\features\plugins.md:709-744` document CLI/gateway injection semantics and the fact that gateway injection is asynchronous and not a completion proof.

Official Hermes documentation used:

- https://hermes-agent.nousresearch.com/docs/developer-guide/plugins
- https://hermes-agent.nousresearch.com/docs/user-guide/features/plugins

Official Codex documentation used:

- https://developers.openai.com/codex/app-server (redirected content title `Codex App Server | ChatGPT Learn`): app-server is the rich-client integration, supports stdio JSONL, requires `initialize` then `initialized`, and documents `thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`, `item/agentMessage/delta`, and `turn/completed`.
- https://developers.openai.com/codex/hooks: hooks are scripts at lifecycle points such as `UserPromptSubmit`, `Stop`, `SessionStart`, and `SessionEnd`; matching hooks run concurrently and are subject to trust review. It does not define a persistent bidirectional command/event transport.
- https://developers.openai.com/codex/plugins/build: the required plugin manifest is `.codex-plugin/plugin.json`; plugins can bundle skills, MCP configuration, and optional app metadata. The Astrorder companion uses the documented app-server instead of inventing an app/connector registration surface.

The Codex app-server page currently labels WebSocket transport experimental/unsupported for production. The artifact therefore uses the documented default stdio transport and a separate loopback Astrorder WebSocket. No remote Codex WebSocket listener is enabled by this code.

## Verification boundary

`backend/tests/test_connector_artifacts.py` tests the hook registration, Hermes injection semantics, turn-ID stream coalescing, Codex JSON-RPC method construction, app-server notification mapping, stop/cancel path, and command-ID non-claim. These are inert source-compatible tests with fake transports/app-server objects.

The local Hermes path is additionally verified with `scripts/verify_native_hermes_local.py`: it starts a temporary loopback FastAPI server, an installed Hermes runtime using `tui_gateway.entry`, and a new owned `source: local` session; Chromium desktop/mobile then authenticate, render the agent/session, and submit one browser command through native `prompt.submit`. The evidence records opaque agent/session IDs, `accepted` command state, and four canonical messages without reading a user conversation or credentials. This does not validate Codex, existing Hermes sessions, Desktop/gateway injection, or a remote SSH host.
