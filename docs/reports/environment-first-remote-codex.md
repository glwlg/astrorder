# Connection-first Agent discovery and remote Codex

## Delivered
- `/api/v1/environments` exposes local and saved SSH environments with peer Hermes/Codex entries.
- SSH discovery reuses validated OpenSSH settings, enforces noninteractive known-host verification, and reads executable availability without starting Agent sessions.
- Explicit per-Agent connect/disconnect selections persist in `agent_connection_choices`; discovery is separate from selection.
- Startup discovery/reconnection runs after the ASGI listener starts, avoiding native callback handshake deadlock.
- Remote Codex uses an owned SSH stdio app-server, native thread IDs, bounded message paging, and remote read-only model metadata. Existing command/approval/model methods share the Codex implementation.
- Desktop and mobile use environment-first connection controls. SSH forms ask transport settings rather than a Hermes-specific configuration upfront.

## Verification
- Frontend: 105 tests passed; production build passed (existing bundle-size warning).
- Backend related regressions: 18 passed, including no-connect discovery, persisted opt-out and remote source/connection identity.
- Isolated mobile browser suite: 14 passed.
- Actual WSL SSH probe discovered both Hermes and Codex; native Codex read 103 threads, latest two items and model metadata.
- Live Chromium exercised discovery, explicit remote Codex connect, authenticated native messages/model reads, disconnect, desktop and mobile layouts; no page exceptions.
- No prompts were sent, no user models changed. Actual remote send/stop/model switching is not claimed; those dispatch paths share protocol-tested Codex implementation.
- Remote Codex returned to its original disconnected state after verification. It remains discovered and ready for user selection.
- Final backup/restart preserved all 263 cached native session identities. Local Hermes/Codex and existing WSL Hermes restored; WSL Codex remained disconnected as explicitly selected.

## Boundaries
- Real remote target verified: WSL/Linux via existing SSH connection. Other SSH operating systems are not claimed verified.
- Codex attachment mapping and permanent deletion remain unsupported.
- Existing compatibility endpoints and legacy settings components remain available internally; the active route uses the environment-first interface.
