# Frontend report — 星序 · Astrorder

## Actual changes

- Replaced the Vite starter screen and unused starter assets with a Mantine/Tabler React application under `src/app`, `src/components`, and feature folders.
- Added authenticated routing for `/chat`, `/chat/:sessionId`, `/monitor`, and `/agents`. The private shell is only mounted after `/api/v1/auth/session` reports an authenticated HttpOnly-cookie session.
- Added responsive shell behavior: desktop sidebar/session rail and chat detail panel; mobile navigation drawer and session detail drawer. Added light/dark Mantine color scheme controls and the 星序 constellation visual language.
- Added contract HTTP client for auth, bootstrap, messages/history, commands, attachments, runtime, and launch. Browser commands use HTTP only; attachments upload through the authenticated multipart endpoint.
- Added read-only WebSocket event stream at `/ws/v1/events?after=<cursor>`, durable cursor tracking, duplicate event-id filtering, reconnect, resync invalidation, and shared Zustand state for agents, sessions, messages, commands, approvals, drafts, and outbox receipts.
- Added scoped identity using `agent_id + session_id`, stable message/event/command IDs, command outbox states, late acknowledgements, unknown delivery state, history paging with prepend anchor preservation, and no optimistic canonical user message.
- Added safe Markdown rendering (`skipHtml`, URL filtering), image/file attachment rendering only for authenticated same-origin URLs, thinking/tool activity, approval controls, capability-based disabling, queue/stop controls, preserved failed drafts, and honest empty/disconnected/unsupported/error states.
- Added monitor cards that read the same runtime store as chat, with status, tool activity, running commands, approval counts, and drill-down routing. No second transcript reconciliation path was added.
- Removed the replaced Vite `src/App.tsx`, `src/App.css`, starter `src/assets/*`, and unused starter `public/icons.svg`; replaced the favicon with a small constellation mark.
- Added `e2e/astrorder.spec.ts` and `playwright.config.ts` for real isolated FastAPI runs. The tests do not provide production fixture agents or messages.

## Verification

All commands below were run from `P:/workspace/glwlg/ai/astrorder/frontend`.

- `npm run test` — exit 0; 8 test files, 23 tests passed.
- `npm run lint` — exit 0 (`oxlint`).
- `npm run build` — exit 0 (`tsc -b` and Vite production build).
- `npm run test:e2e` — exit 1 before test execution because the installed Playwright package has no local Chromium executable (`chrome-headless-shell.exe`); the command suggests `npx playwright install`. No browser was installed or global state changed.
- `curl -fsSI http://127.0.0.1:5173/` — exit 0 while the local Vite dev server was running.

The production build reports a non-fatal Vite chunk-size warning for the current single application bundle. It does not affect the build exit status.

## Behavioral test coverage

The Vitest suite covers:

- identical legitimate sends surviving an old bootstrap snapshot;
- same session IDs separated by agent scope;
- same and different attachment selections, including image-only commands;
- stable command IDs and no automatic retry after ambiguous HTTP failure;
- late command acknowledgement updating the exact outbox entry;
- queue acknowledgement remaining distinct from accepted send;
- accepted commands not becoming fabricated canonical messages;
- duplicate event replay, durable cursor reconnect, and heartbeat rejection;
- sticky-bottom follow, genuine browsing pause, manual return, and scroll-to-bottom behavior;
- safe raw-HTML, executable-link, and protocol-relative-link handling;
- unauthorized HTTP errors;
- isolated authenticated/unauthenticated app states, route isolation for reused IDs, and monitor/chat consistency.

`src/app/App.test.tsx` explicitly labels its intercepted HTTP/WebSocket data as `isolated browser API fixture scope — not production Agent data`.

## Browser evidence

Using the real Vite dev server and the browser harness:

- Desktop viewport: `1258x622`. `/chat` rendered the Chinese authentication screen and a server error banner (`请求失败（502）`) because FastAPI was not running. No Agent, session, or message was invented.
- Mobile viewport: `390x844`. `/chat` rendered the same authentication screen with `document.documentElement.scrollWidth === clientWidth`; no horizontal overflow was present.
- No authenticated chat/monitor/Agent-settings browser evidence was claimed because the backend was not running and Playwright's bundled browser executable was absent. The authenticated UI is covered only by isolated component tests until integration runs against FastAPI.

No live Desktop/Overlook process was restarted or modified. The Overlook project was used only as a read-only interaction reference; no DOM scraping or Desktop bridge dependency was introduced.

## API assumptions and proposals

- The frontend uses the frozen `/api/v1` HTTP prefix and `/ws/v1/events?after=<cursor>` contract exactly.
- `GET /sessions/{id}/messages` returns chronological items and `next_cursor` points to an older page. TanStack Query pages are reversed before merging so older items prepend without matching on text, time, or display order.
- Event `data` is the full object for the declared event type. Commands remain distinct from canonical messages; a command acknowledgement does not create a user message.
- Attachment URLs are authenticated same-origin download routes. External or malformed attachment URLs are shown as unavailable.
- `POST /api/v1/auth/session` accepts the token in the request body and establishes the HttpOnly cookie. The browser sends later requests with `credentials: include`; no token is stored in localStorage or placed in a URL.
- No contract change proposal is needed for the current frontend MVP.

## Unsupported or unimplemented

- Native Hermes/Codex lifecycle integration is not implemented or verified by this frontend session. The UI does not claim native Agent integration from fixture events; it only renders server/connector-reported Agent objects and capabilities.
- Agent register/attach has no endpoint in the frozen contract, so no invented register control was added.
- Managed launch is shown only when `/runtime` reports an available item with `launch`; workspace validation remains server-side. Unsupported or rejected launch responses stay visible as errors.
- Real FastAPI HTTP/WS, attachment, replay, approval, and native connector behavior still require the integration session to run its isolated backend/connector checks.
- Playwright scenarios are ready for that integration run, but local execution is blocked by the missing Playwright browser binary noted above.
