# Native observers and capability reconciliation

## Delivered and deployed

- Effective Hermes capability responses now include registered native history/stop channels instead of displaying only the plugin's static declaration. Stop remains scoped to a current runtime handle; no cross-host control claim.
- Codex passive command hooks installed idempotently in Windows and the already-connected WSL environment. Existing handlers are preserved; prior changed files are backed up. Ten events: SessionStart, SessionEnd, UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest, SubagentStart, SubagentStop, Stop, Interrupt.
- Hook writes bounded metadata to a local spool, emits no stdout/stderr or authorization decision, performs no network request, and receives no service credentials. Prompt text, tool arguments/results and assistant bodies are discarded. Spool caps count/age. Astrorder polls locally or through existing strict-host-key SSH; durable event IDs deduplicate delivery. Native IDs are retained; unknown sessions require native thread/read before catalog insertion.
- Authenticated observation status/recent records, install/update buttons, desktop/mobile details and browser notifications added. Tool completions do not individually notify. Old observations do not trigger new alerts. Hook PermissionRequest is informational and directs user to the native client, not an actionable approval record.
- Codex image input mapping and native thread/delete implementations added with tests. Images use data URLs so Windows filesystem paths are not sent to Linux. Only supported image attachments are accepted; documents/audio fail before submission. Native delete requires non-active locally tracked state, native acknowledgment, and absence from both archived/non-archived directories before clearing the projection.
- Local and remote hook configuration readback each reports 10 configured events, installed=true, trusted=false, needs_review=true. User must review exact definitions through native /hooks; no trust-bypass flags or trust-database edits used.

## Verification

- Backend complete suite: 115 passed, 2 dependency warnings. Added observer integration subsequently passed; final targeted set including environment transport and new functionality: 14 passed.
- Frontend: 114 tests across 50 files passed; production candidate build passed with pre-existing bundle-size warning.
- Isolated browser: 18 checks passed. Protocol tests are not real native model execution.
- Real production smoke: assets, HTTP authentication, origin rejection, WebSocket, four restored Agent connections, no Vite requests/page exceptions passed.
- Real observer page: two Codex panels show native-review-required, Hermes effective history/stop capabilities returned, no raw TASK_EVENTS label, no page exceptions.
- Single service remains on 30001. Last restart preserved 276 pre-restart session IDs and restored four connections; backups under ignored .runtime/backups.

## Not complete / gated

- Hooks are installed and natively discovered, but native trust is pending in both Windows and WSL Codex. No real hook lifecycle or desktop notification is claimed end-to-end until trusted and exercised.
- Codex image submission and permanent deletion have implementation/schema/unit evidence, not a real disposable native session/model execution test.
- Hermes attachments and actionable native approval round trips remain unimplemented. Existing Hermes plugin observation is unchanged; no new Hermes host hooks installed in this batch.
- Capability matrix is still coarse Agent-level plus explanatory limits, not a complete per-session available/unavailable/native-unsupported matrix.
- No native prompts, real user-session deletion, automatic approval, Hermes core edits, user client restarts, commit or push.
