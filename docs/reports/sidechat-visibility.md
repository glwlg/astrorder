# Side-chat catalog visibility regression

## Root cause and changes

- The create API set `ephemeral`, but `SessionRow`, `_session_wire`, and `SessionModel` dropped it. Both native discovery (`upsert_session`) and connector events (`_upsert_session_in`) need the field.
- Add an additive SQLite Boolean column. Preserve true across missing/false native updates, including the native insert-conflict path. This is projection metadata, not a claim that the native session is memory-only.
- Preserve Codex's native `thread.ephemeral` before broadcasting the first thread projection.
- Upgrade only the exact legacy application marker `[侧边聊天]`; no substring matching or native content deletion. Subsequent title changes retain the flag.
- Share frontend temporary-session classification between `selectSessions` and rail grouping. Exclude temporary sessions from desktop/mobile catalogs, navigation candidates and the sidebar total, while retaining their scoped store data for the side composer and events.

## Executed verification

- New backend native/connector regression failed with `KeyError: ephemeral` before the fix.
- Legacy migration regression failed with `False is True` before the fix.
- Codex notification regression failed on missing `ephemeral` before projection was fixed.
- Frontend selector and legacy grouping tests reproduced the leaked entries before their fix.
- `npx vitest run`: **69 files, 222 tests passed**.
- `npm run build -- --outDir ../.runtime/sidechat-candidate`: passed, including TypeScript.
- Backend: `pytest -q tests/test_ephemeral_sessions.py tests/test_sidechat_fork.py tests/test_native_identity.py tests/test_control_server.py tests/test_codex_connection.py tests/test_session_approval_mode.py`: **43 passed**.
- `import astrorder.api`: passed.
- Focused oxlint and ruff: passed.
- `playwright test e2e/sidechat.spec.ts --project=desktop` on an inert loopback FastAPI fixture: **2 passed** (light/dark). Each covers desktop open/send/close/reload, stable sidebar total, a legacy response with no true lifecycle flag, native-shaped automatic title changes without lifecycle metadata, and mobile drawer exclusion. Uses only disposable fixture sessions, never user sessions or a real model.
- Existing non-failing warnings: Node localstorage-file, JSDOM canvas, Starlette/httpx/AnyIO deprecations, Vite outDir and bundle size.

## Rollout and limits

Frontend published without restarting the service. Read back index and its assets from port 30001 and compared their bytes with the tested candidate; public health passed. Main asset: `index-DxMIXZbB.js`. Static backup: `.runtime/sidechat-static-backup-71807ce5`.

The production database read-only check found the reported exact legacy marker. The deployed frontend hides this marker even with the older backend response.

**Backend rollout deferred**, not completed: the last safety check found the current user conversation running, with no pending Astrorder commands. The confirmed listener was PID 37304 running `scripts/run_production.py` from the repository, using root `astrorder.sqlite3`. No processes were stopped and no production schema or native history was edited. Activate the backend patch only with a safe idle restart, backing up the actual DB and verifying connection restoration. Do not use a blanket port-kill restart.

This fix does not implement or prove native Hermes context inheritance, ephemeral runtime storage, native close/TTL deletion, or side-tab lifecycle retention. D1 remains open.
