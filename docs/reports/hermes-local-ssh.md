# Hermes local + SSH v1 integration report

## Scope and status

This run implemented the local Hermes connection path and SSH-only remote settings. It did not access any existing Hermes conversation, Hermes Desktop, gateway, Overlook, Codex session, credential file, or remote host.

Verified:

- Local Hermes discovery reports safe runtime metadata and a real state progression (`discovered`, `installed`, `connecting`, `connected`, `offline`, `error`) through authenticated `/api/v1/connections`.
- An explicit local connect installs only the checked `astrorder-hermes` wrapper into the active Hermes profile, enables it with the documented Hermes CLI, and starts an Astrorder-owned `tui_gateway.entry` process. It does not modify Hermes core, other profiles, model settings, Desktop, gateway, or Overlook.
- The local connector reached the preview server over `ws://127.0.0.1:30002/ws/v1/connector`; `wmic` verified the owned runtime process command as `python.exe -u -m tui_gateway.entry`.
- Native TUI JSON-RPC creates a fresh `source: local` session. The durable Hermes session ID used by Astrorder is mapped server-side to the private live TUI session ID; no browser value selects a different native session.
- Browser `send` for this owned session goes through documented TUI-gateway `prompt.submit`, not terminal text parsing, a one-shot CLI wrapper, ACP, or an invented websocket endpoint. The successful result is durable `accepted`, not a fabricated `completed` state.
- The backend marks all persisted connector rows `disconnected` at process startup before new live connector hellos, preventing old snapshots from appearing controllable.
- SSH v1 stores only host/port/user/alias/identity-file reference/remote Hermes path/workspace. It rejects shell-shaped hosts and unknown payload fields, never accepts passwords or private-key contents, and uses fixed `ssh -G` argv only for local configuration validation.
- Agent Settings has local discovery/connect/disconnect controls and a persisted SSH configuration form. It exposes no connector secret, browser secret, remote websocket URL, password, or private-key contents.

Unsupported or intentionally not claimed:

- Remote SSH connection is not established until an operator provides a trusted host or alias and explicitly provisions the same native connector remotely. The current Connect action returns a clear non-connected result; no remote host was guessed or contacted.
- Codex native lifecycle remains unverified.
- Existing Hermes sessions, Desktop/gateway injection, Overlook, and any remote machine were not accessed.
- The local connector advertises only `chat` and `events`; attachments, queue, stop, approvals, and history remain disabled.
- Hermes does not expose the browser command ID in its canonical observed messages. The command remains `accepted`; Astrorder does not fake a `completed` state or infer identity from text, timing, or order.
- Cleanup of a throwaway `connectors/hermes/.venv` created by an incorrect test-environment command was blocked by the terminal approval gate. It is not used by Astrorder; remove that exact directory only after approval if repository hygiene requires it.

## Changed implementation

- `backend/src/astrorder/connections.py`: local runtime discovery, active-profile wrapper installation, owned TUI gateway process/RPC lifecycle, durable-to-live session mapping, local command outcome handling, SSH validation/persistence controller.
- `backend/src/astrorder/service.py`, `models.py`, `store.py`, `schemas.py`, `api.py`, `main.py`, `config.py`: authenticated connection endpoints, non-secret SSH table, native command handler, stale-connector startup state, loopback 30002 defaults, PUT CORS support.
- `.hermes/plugins/astrorder-hermes/`: project-local native plugin wrapper and manifest. The wrapper loads connector source from the explicitly supplied project root and was checked by Hermes Plugin Doctor.
- `connectors/hermes/astrorder_hermes_plugin/`: registration-time connection state and observed Agent status; local owned TUI send is handled by the documented TUI gateway path.
- `frontend/src/features/agents/AgentsPage.tsx`, API/types/hooks: local connection state and SSH v1 configuration UI.
- `frontend/src/index.css`: removed the custom `position: relative` override that made Mantine's desktop navbar participate in normal flow and pushed the main session page below the viewport.
- `frontend/e2e/native-hermes-local.mjs` and `scripts/verify_native_hermes_local.py`: controlled real-runtime Chromium verification with desktop/mobile artifacts.
- README/config/examples/connector docs/contract/Playwright defaults now use frontend `30001`, backend `30002`; Astrorder does not launch on `5173`.

## Native evidence

Final isolated native run:

- Command: `uv run --project P:/workspace/glwlg/ai/astrorder/backend python P:/workspace/glwlg/ai/astrorder/scripts/verify_native_hermes_local.py`
- Result: exit 0.
- Temporary loopback server: `http://127.0.0.1:58407` (stopped after the run; only TIME_WAIT sockets remained).
- Agent: `local-hermes-8547e8da-4a12-4501-a28b-815305823b28`, `connected`.
- Fresh session: `20260907_111246_2df0e0`.
- Result: four canonical messages and exactly one browser command in `accepted` state.
- Evidence: `.runtime/native-hermes-evidence/run-00f0fd52a4fc472a8c491d749e55035d/evidence.json`.
- Artifacts: `native-hermes-agents-desktop.png`, `native-hermes-session-desktop.png`, `native-hermes-agents-mobile.png`, and `native-hermes-session-mobile.png` in the same directory.
- Chromium visual review confirmed the connected Agents card and actual native session at desktop and mobile widths without horizontal overflow. The desktop session screenshot contains the native smoke reply and the browser-originated native prompt reply.

The native test uses newly generated temporary browser/connector credentials internally. No preview token, user credential file, or existing conversation was read or printed. Windows emitted a non-fatal `ConnectionResetError [WinError 10054]` while the temporary server/browser websocket was being torn down; the verification still exited 0 and the temporary listener was gone.

Hermes source/docs verification:

- `hermes plugins doctor astrorder-hermes --ci`: manifest parsing, import, and seven hook registrations passed.
- `hermes plugins list --enabled --user --plain`: `astrorder-hermes` is enabled in the active profile.
- Read-only source inspection confirmed `PluginContext.inject_message` requires an existing gateway session key and explicit gateway-injection permission, so owned TUI sessions use the documented `tui_gateway` JSON-RPC `prompt.submit` protocol instead.
- Official references: Hermes Plugin Guide and Programmatic Integration documentation.

## Current preview

- Frontend Vite: `http://127.0.0.1:30001` — owned handle `proc_1aa02fd92c54`.
- FastAPI preview: `http://127.0.0.1:30002` — owned handle `proc_b054f2877704`, loopback-only, health returns the expected protocol-v1 JSON.
- Preview browser token was regenerated during the controlled restart and was never read or printed.
- Current live preview Agent: `local-hermes-040ab9c6-ae2c-4b2f-83d7-44f0c314d5dd`, `ready`.
- Current owned preview session: `20260907_112710_026195`, `idle` at final inspection.
- Previous owned preview Agent rows remain as `disconnected`, not ready/controllable.

## Checks

- Backend full suite: `uv run pytest -q` — `36 passed`; upstream Starlette/httpx/AnyIO deprecation warnings only.
- Backend, connector, and script Ruff: three `All checks passed!` results.
- Frontend: `npm run test` — `10` files / `28` tests passed; `npm run lint` passed; `npm run build` passed. Vitest emitted only the known local `--localstorage-file` warning and Vite emitted its non-fatal bundle-size warning.
- Existing isolated built-SPA regression: `npm run test:e2e` against a private loopback server and inert connector — `12 passed`; `scripts/verify_isolated_security.py` — `isolated security gates passed`. Those owned test processes were stopped; port 30102 has no listener.
- Root orchestration tests: `python scripts/test_orchestrate.py` — `4` tests passed.
- Hermes and Codex connector wheels: `uv build connectors/hermes` and `uv build connectors/codex` passed.
- Backend targeted connection tests: `6 passed` after adding local discovery/profile-install, native durable/live ID mapping, command identity, SSH validation, and stale-connector coverage.
- Hermes Plugin Doctor: passed.
- Native local Chromium verification: passed with desktop/mobile artifacts.
- Native-runtime acceptance above is backed by real connector/session/browser evidence, not by an exit code alone.

## User action required for remote SSH

Provide configuration in Agent Settings before a remote attempt:

1. A trusted host or existing OpenSSH config alias, plus port and user when applicable.
2. A host key already verified in the operator's known_hosts policy; do not disable verification or silently accept a changed key.
3. An OpenSSH agent/key already available to the operator, or an identity-file reference. Astrorder will not read or store the key contents.
4. The remote Hermes executable path and workspace.
5. Explicit authorization to install/enable/start the Astrorder native connector on that remote host and to test it with a new isolated session.

Until then, remote SSH validation is only local `ssh -G` configuration validation and remote connection remains unverified.
