# 星序独立移动端 — Overlook 对照验收

Reference: `P:/workspace/glwlg/ai/Hermes-plugins/overlook/server/mobile.html`, paired live page `http://127.0.0.1:9999/`.

## Non-negotiable scope
- Independent mobile component tree, shell, composer and transcript; do not shrink Desktop AppShell/ChatPage.
- Shared native-ID store/API/command semantics only. Keep fixed project order and tool-notification suppression.
- Never send prompts or destructive controls to existing working sessions during comparison.
- Browser automation is not real-device proof for microphone, mobile keyboards, iOS PWA/Web Push or background suspension.

## Acceptance inventory
Status starts **pending**; implemented does not mean exercised.

| Area | Operations | Status |
|---|---|---|
| Shell | portrait layout, safe areas, top status, theme light/dark/system, refresh | pending |
| Session drawer | bottom dock, backdrop/close, projects, fold, search, all/unread/open/pinned/24h filters | pending |
| Navigation | select session, left/right swipe, route/source isolation | pending |
| Transcript | text/markdown, thinking/tool packs, individual folds, earlier messages, follow/pause/bottom arrow | pending |
| Message actions | long press, copy, select text sheet, quote/cancel quote, code copy/wrap | pending |
| Composer | multiline, clear, draft per session, send/stop, delivered/error state | pending |
| Attachments | choose/remove, image preview/lightbox, text+image, image-only | pending |
| Voice | permission, recording/stop, playback, cancel, transcript/draft | pending |
| Quick actions | continue, real status (not LLM /status), progress summary, stop tasks | pending |
| Activities | active cards, details/logs, log copy/bottom, individual stop | pending |
| Queue | queued text/attachments, persistence, send-now, ambiguous delivery protection | pending |
| Models | native list/search/select/read-back | pending |
| Notifications | explicit permission, no replay storms, background limitations | pending |
| Resilience | offline drafts, reconnect, visibility/online recovery | pending |
| PWA | manifest/icons/start URL, installation | pending |

## Executed first-pass evidence

- `frontend/e2e/mobile-comparison.mjs` operated the paired Overlook page and authenticated Astrorder `/mobile` at 390×844. Latest run: 12 checks passed, no failures. JSON and screenshots: `frontend/test-results/mobile-parity/`.
- Exercised: theme cycle; drawer open/close, filters and search; independent shell/no desktop DOM and no horizontal overflow; multiline draft/clear; status sheet; file choose/remove without sending; voice sheet open/cancel without microphone permission; message selection/quote/cancel; model catalog/search without changing a working session.
- `npm test`: 61 existing frontend tests passed. `npm run build`: passed (existing bundle-size warning).
- `test_native_mobile_controls.py` and `test_native_read_reconnect.py`: 4 backend tests passed; this proves adapter contracts, not live model switching.
- Original and target screenshots were inspected. Main controls are visible without overflow. Pixel parity has NOT been achieved: drawer row treatment, avatar/icons, queue/status details and recording UI still differ.

## Still required before full acceptance

- Dedicated disposable-session send/image-only/stop/queue drain and reconnect tests. Existing work sessions were not used for destructive/send tests.
- Live model switch plus native read-back on a disposable session (catalog/search was tested).
- Actual touch swipe/long-press, microphone recording, async image resizing and mobile keyboard tests, including iOS.
- Notification permission, PWA manifest/installation and background Web Push; none are claimed to be verified by desktop Chromium.
- Complete visual alignment of all sheets, pinned breakout, model metadata and selection states.

No 1:1 completion claim is permitted until every operation has an evidence entry or an explicitly accepted limitation.
