# Protocol v1 — frozen baseline for parallel development

All JSON is snake_case. Base /api/v1. Timestamps ISO-8601 UTC. Message, session, and command IDs are opaque strings scoped by agent_id + session_id; never text-derived. Connector source event IDs are opaque and deduplicated by (agent_id, event.id), while server-assigned cursors are global and monotonic. Objects below define stable wire shapes, not backend module structure. Additive optional fields allowed; incompatible changes require integration phase reconciliation.

## Shared types
Capability = chat | stop | queue | attachments | approvals | launch | history | events.
Agent = {id, kind: hermes|codex, name, status: disconnected|connecting|ready|error, capabilities: string[], limitation: string|null}.
Session = {id, agent_id, title, workspace: string|null, status: idle|running|waiting_approval|error, updated_at}.
Attachment = {id, name, media_type, url}; URL is authenticated same-origin download route, not arbitrary filesystem path.
Message = {id, session_id, agent_id, role: user|assistant|system|tool, kind: message|thinking|tool, text, attachments: Attachment[], created_at, command_id: string|null, tool: object|null}. Canonical ID assigned by connector/server from reliable identity; not text equality.
Command = {id, session_id, agent_id, action: send|enqueue|stop|approve|cancel, state: received|queued|accepted|running|completed|failed|unknown|cancelled, text, attachments: Attachment[], created_at, error: string|null, target_id: string|null}. Command.id is browser-generated UUID and idempotency key. Approval/cancel target is target_id; reject unsupported actions. Commands are NOT Message objects. UI renders durable outbox receipts separately from the canonical transcript.
Event = {id: string, cursor: integer, type: string, agent_id: string|null, session_id: string|null, data: object}. Durable cursor monotonically increases, duplicate event id ignored, bounded replay with snapshot fallback. Event types: agent.upsert, session.upsert, message.upsert, command.upsert, approval.upsert, resync_required; data is corresponding full object, not ambiguous text delta. Internal streaming adapters can coalesce into message.upsert.
Error response = {detail: string}. Do not expose stack, tokens or local private paths.

## Browser HTTP
GET /health -> {status:"ok", service:"astrorder", protocol_version:1}; unauthenticated, no private state.
GET /api/v1/bootstrap -> {protocol_version:1, agents:Agent[], sessions:Session[], cursor:integer}; authenticated. Empty lists mean none connected, not mocked sample data.
GET /api/v1/agents -> {items:Agent[]}.
GET /api/v1/sessions?agent_id=optional -> {items:Session[]}.
GET /api/v1/sessions/{id}/messages?agent_id=REQUIRED&before=optional&limit=50 -> {items:Message[], next_cursor:string|null} chronological items; opaque history cursor not UI row count.
GET /api/v1/sessions/{id}/commands?agent_id=REQUIRED -> {items:Command[]}.
POST /api/v1/commands body {id,agent_id,session_id,action,text:"",attachment_ids:[],target_id:null} -> Command. Same id+payload returns existing command, changed payload with same id ->409. Unknown agent/session ->404; unavailable or unsupported ->409 without losing stored attempted command when appropriate. If a connector write is ambiguous, the server returns the persisted Command in state unknown rather than converting it to failed or retrying it. No successful execution claim before connector confirmation.
POST /api/v1/attachments multipart file -> Attachment (bounded content types/size). GET /api/v1/attachments/{id} authenticated.
POST /api/v1/auth/session body {token:string} -> {authenticated:true}; validate configured secret, issue HttpOnly SameSite cookie. GET /api/v1/auth/session -> {authenticated:boolean}. DELETE ->204. No token in URL or localStorage. Reject unsafe cross-origin requests. Local dev may use Authorization: Bearer via explicit test/client API; browser uses cookie.
GET /api/v1/runtime -> {items:[{kind,available,capabilities,reason}]}.
POST /api/v1/runtime/launch body {kind,workspace} -> {agent_id,status} only when /runtime reports launch as available; only allowlisted runtime/workspaces, no arbitrary shell command; unavailable capability ->409/501 with explicit explanation. Current implementation intentionally reports managed Hermes and Codex launch unavailable: a detached CLI/app-server process is not a connected Agent.

## Browser WebSocket
/ws/v1/events?after=<cursor> authenticates browser cookie; validate Origin. Frame = Event. On connect replay events after cursor, then live. Never submit commands over WS: HTTP is single browser command channel. On resync_required refetch bootstrap AND selected transcript/outbox. Heartbeat optional protocol {type:"ping"}/{type:"pong"}, never mistaken for Event.

## Embedded Connector boundary
/ws/v1/connector requires separate role credential in Authorization header, not browser cookie. Initial hello {type:"hello",protocol_version:1,agent:Agent}; after server validation connector may send {type:"event",event:Event} (server assigns canonical cursor, validates identity). Server sends {type:"command",command:Command}. A connector may send additive {type:"pull"} to request intentionally queued enqueue commands after reconnect; unknown commands are never pulled or resent. Connector confirms via command.upsert states with SAME command id. Never fabricate underlying message-command association. Define/document exact missing details in backend connector README and report for integration; browser contract remains unchanged.
Connector connects from actual Hermes/Codex extension lifecycle, not Desktop renderer. Any future process launch is supervision only, not the chat transport. Unsupported native lifecycle hooks must be documented with source evidence and capability disabled. A test connector proves protocol, NOT native integration.

## Safety and semantics
Server defaults 127.0.0.1:8765, auth fail-closed for private endpoints when not configured. No exposure of secrets in /health. Cookie Origin validation for mutations/WS. Attachments never arbitrary local reads. No automatic resend after ambiguous submission; HTTP idempotency alone cannot prove native exactly-once across a crash. Preserve unknown and require explicit reconciliation.
