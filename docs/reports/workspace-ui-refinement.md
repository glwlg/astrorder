# Workspace UI refinement

## Shipped

- Desktop defaults to navigation + conversation. Session details open on demand, can be pinned into a side panel, and can be collapsed again. Pending approvals retain a visible entrypoint in the heading.
- Shared 860px maximum reading/composer column, document-style assistant responses, subtle user bubbles, quieter project grouping, two-line session rows and a compact global header.
- Runtime summary hides unknown fields until expanded; preserves unknown rather than inventing zero. Reported fields, queues and task drill-down remain accessible.
- Mobile remains an independent layout. Session navigation and new-session entrypoint move to the header. Settings and session actions use accessible menus. Model, attachments, voice and send/stop share one composer. Removed the permanent quick-action row and bottom session-count dock.
- Light/dark semantic colors are shared across layouts. Narrow viewport bounds and input controls are checked with real Chromium.
- Exact native interruption notices are localized with original evidence expandable. User local image references become filename cards with explicit unavailable-preview wording and expandable original references; no arbitrary local file access is introduced. Attachment-only mobile messages no longer render empty text bubbles.
- Native identifiers, ordering/grouping logic, outbox semantics and production backend application code are unchanged by this UI change.

## Verification

- `npm test`: 49 files / 112 tests passed. Detail-panel and unknown-runtime assertions failed before implementation, then passed; image-reference presentation has focused tests.
- `npm run lint`: exit 0; existing warnings remain (hooks, component exports and fixture cleanup), not claimed warning-free.
- `npm run build -- --outDir ../.runtime/ui-dist`: exit 0. Existing large-chunk warning remains. Candidate assets were built outside the served directory.
- `ASTRORDER_E2E_STATIC_DIR=... node e2e/mobile-isolated.mjs`: 18 checks passed, 0 failed. Includes inert protocol sends/stop, attachment replay, queue preservation, pagination, models, Agent filters/creation, details pin/unpin, light/dark and narrow/landscape layouts. Fixture polling explicitly avoids real local discovery.
- `node e2e/production-smoke.mjs` with credentials passed only through stdin: production assets, HTTP authentication, allowed/foreign origins, WebSocket and restored connectors passed; zero Vite requests and page exceptions.
- `node e2e/ui-refinement-live.mjs` with stdin credentials: 3 read-only UI checks passed against the real current Hermes conversation. Current tail was tool-only, so verification paged backwards until actual conversation messages were present rather than weakening transcript assertions.
- Real screenshots inspected under `frontend/test-results/ui-refinement-live/`; isolated light/dark screenshots under `frontend/test-results/mobile-parity/isolated/`.

## Rollout

Backed up the actual SQLite database and old static build. Published hashed assets before atomically replacing the index; retained old assets for open browser tabs. Restarted only the identified `run_production.py` service. Initial Hermes rediscovery failed; a version probe warmed the existing CLI, then the prior enabled connections were restored and their ready state read back. The pre-restart database confirms all four restored Agent connections had been ready.

Readback preserved all 271 pre-release native session IDs and confirmed four Agent connections. Only 30001 listens among the production/development/test web ports 30001, 30002 and 30013. Detailed local backup/readback metadata is in `.runtime/ui-release-result.json`.

No real prompt, native model change or test-session creation was performed by the live UI checks. Connector reconnect itself uses its existing native lifecycle. No Hermes core changes, commit or push. Public Cloudflare Access login was not bypassed; real-phone keyboard/recording is not claimed verified.
