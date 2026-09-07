# 星序 · Astrorder — development brief

Brand: 星序 · Astrorder (provisional). Meaning: 群星各有所长，协作自有秩序. A local-first independent control workspace for Hermes and Codex, usable from phone and PC. This is a new project, not a rename of the running Overlook plugin.

## Architecture
React + TypeScript + Vite; Mantine Core/Hooks/Notifications and Tabler icons; React Router, TanStack Query for HTTP state, Zustand where useful for transient state, TanStack Virtual for large views, react-markdown + remark-gfm for safe markdown. FastAPI + Pydantic + SQLAlchemy + SQLite; authenticated persistent event/command service. Serve compiled frontend from backend for a single deployed process. Dev servers are separate.

Hermes must work without Hermes Desktop: connector lives in the Agent extension/runtime lifecycle. Codex connector uses documented plugin/hooks plus a persistent companion if needed. Starting a process is allowed for supervision, but parsing CLI stdout or ACP-only integration does NOT satisfy embedded integration. Verify actual extension surfaces before writing adapters. Provide capability reporting and actionable unsupported states if the installed APIs cannot support a function; do not invent internal APIs.

## MVP features
1. Responsive app shell: PC sidebar/chat/detail panel, mobile drawer/chat/sheets. Chinese primary UI, light/dark, keyboard and screen-reader accessible.
2. Chat: session list scoped by agent and workspace; authoritative messages, thinking/tool activity, safe markdown, image attachments, composer, outbox and queue, stop, approval only when supported.
3. Monitor room: independent route, PC multi-session grid and mobile single-column cards; live status, tools, pending approvals, drill-down into chat. Shares data with chat, no second message reconciliation pipeline.
4. Agents: Hermes/Codex connection status, supported capabilities and limitations, register/attach and managed launch if genuinely supported. No simulated connected agents in production.
5. Server: durable commands with idempotency, event IDs/cursor replay, sessions/history paging, role-authenticated connector channel, browser authentication/pairing, safe process supervision of owned processes only.
6. Reconnect and failure UX: preserve drafts/attachments; delivery unknown stays explicit; no automatic duplicate submissions; mismatched snapshot/event cursor triggers resync.

## Message and scrolling acceptance
- One accepted command must not create parallel optimistic/inflight/durable user bubbles. Separate outbox receipt from canonical transcript.
- Two identical legitimate commands stay two; no content/time/order based dedupe.
- Text+image, image-only, attachment transformation, late ack, old snapshot, tail/full replay, second client and reconnect tests required.
- Default follow bottom during streaming; genuine upward browsing pauses; manual return/arrow resumes. Async image/composer/keyboard layout must not impersonate user intent. Preserve prepend anchor, scope state per session.

## Reference, NOT writable
P:/workspace/glwlg/ai/Hermes-plugins/overlook: mobile UI and Desktop monitor-room interaction reference. Read focused functions/screens; do not migrate monolithic file or desktop bridge dependency. Existing tests/history are not proof of correctness: previous fixes passed idealized fixtures but failed real phone behavior.

## Delivery stages
A. Bootstrap installed, built, tested frameworks and frozen API contract (coordinator).
B. Two independent named Hermes sessions, both provider ocx / model gpt-5.6-luna / reasoning max: frontend and backend; disjoint ownership.
C. Only after both exit, a NEW named Hermes session provider ocx / model gpt-5.6-terra / reasoning max runs integration. It reads both reports, independently runs tests, fixes mismatches, exercises browser and real HTTP/WS with inert connectors. Must distinguish a working framework/MVP from verified real Agent runtime integration.
D. Integration writes docs/reports/integration.md with exact commands/results, browser responsive results, native connector evidence, security gates, remaining blockers. No automatic commit, plugin install, destructive restart, or public deployment.
