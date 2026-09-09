# Codex connection and transcript delivery

## Delivered

- Desktop connection manager includes local Codex connect/disconnect controls; mobile has a Codex connection button in its independent header.
- Native initialization, account-state inspection, complete thread catalog discovery and command-handler registration precede the connected state. Connecting does not create a thread or send a prompt.
- Native thread/item IDs are retained. Message reads request the newest two records and older pages of twenty through native paging.
- Model display uses only model/provider/branch metadata from the native database under the runtime-reported codexHome. This avoids the observed rejection when attempting to resume an already-active thread for display.
- Explicit command handling includes text turns, scoped interruption and user-confirmed approvals. Stop receipts wait for target-turn completion. Model changes require native settings confirmation.
- Late HTTP receipts cannot downgrade terminal command events: backend receipt updates preserve progress atomically and frontend reconciliation retains terminal outcomes.
- Thought/tool packs and the desktop activity sidebar default closed. Mobile message actions use an anchored bubble menu. Runtime facts include connection, Agent type, model, branch and native ID.

## Verification

- Frontend: `npm test` — 89 passed; `npm run build` passed.
- Backend selected connection, paging, control and connector-artifact suite — 77 passed.
- `node e2e/mobile-isolated.mjs` — 12 passed against an owned isolated FastAPI instance and inert connector.
- `node e2e/codex-native-readonly.mjs` — 5 passed against real installed Codex through an isolated Astrorder backend: desktop connection/readback, newest native items, older-page cursor, native model metadata, mobile controls/disconnect. Catalog count and unique native IDs matched at 102 sessions. No browser exceptions.
- `uv lock --check` passed; targeted production Codex Python modules passed Ruff; `git diff --check` passed. Frontend lint has warnings but no errors; the build still reports a large bundle warning.
- Native probes sent no prompts and did not switch models, rename or delete existing threads. Text-send, interruption, model-change confirmation and receipt races were tested with isolated protocol fixtures, not claimed as native model-execution verification.

## Evidence

- `frontend/test-results/codex-native/results.json`
- `frontend/test-results/codex-native/connected-desktop.png`
- `frontend/test-results/codex-native/connected-mobile.png`
- `frontend/test-results/mobile-parity/isolated/results.json`
- `frontend/test-results/mobile-parity/isolated/message-bubble-menu.png`
- `frontend/test-results/mobile-parity/isolated/runtime-facts-desktop.png`

## Limits and rollout

- Local Codex only. Remote Codex, native attachment mapping and permanent deletion remain unintegrated; archive is not substituted for deletion.
- Existing threads held by another native client are not forcibly taken over or interrupted. Their stored model metadata remains readable without resume.
- Primary backend on port 30002 is healthy but its OpenAPI does not yet contain the Codex routes. It was not restarted. Deployment of these backend changes still requires an authorized Astrorder backend restart.
- No commits/pushes, Hermes-core edits, credential-file inspection or unrelated process termination were performed.
