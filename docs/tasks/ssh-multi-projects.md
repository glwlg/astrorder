# Complete SSH multi-connection and full project/session integration

Root P:/workspace/glwlg/ai/astrorder. Resume 20260906_235516_6f65df. Existing code and uncommitted work must be preserved. Read this latest task rather than re-running old integration gates. Current configured model.context_length=300000, compression.threshold=0.5 (coordinator verified with hermes config get). Honor those settings; do not increase context or change global model configuration. Check actual effective context at startup if possible. Keep reads targeted, not whole large source files. No additional modifying agents.

Latest user complaints/requirements:
- SSH won't connect; with working SSH authorization Astrorder should automatically install its plugin, not require manual remote provisioning.
- Multiple independently managed SSH connections, no artificial one-connection limit.
- Existing sessions are missing. Same project sessions are split into three groups.

Confirmed root causes:
1. backend/src/astrorder/connections.py connect_ssh just calls ssh -G validation and unconditionally raises 409. This is not a remote attempt or permission issue. Implement real transport, do not replace it with another placeholder.
2. singleton SSH row/controller/API/form. Migrate existing saved config non-destructively to a stable connection ID; CRUD collection UI, independent connect/test/disconnect/delete and status/errors/process ownership per connection. Bound resource usage reasonably without hardcoding one connection. Updating an alias must not redirect another live connection.
3. SessionRail.tsx groups by ephemeral agent_id. Local connect currently produces a new local-hermes UUID per runtime and only a freshly created session. Need stable source identity (connection/device + active profile) separate from runtime incarnation, and project grouping within it. Same source+project merges display despite reconnect; same path on different machines/profiles must never merge. Preserve agent+session opaque routing and explicit mappings; do not dedupe messages or sessions by text/title alone.
4. Existing history/session discovery is not implemented. User now requests access to existing sessions of configured local/remote Hermes source. Use supported native session/project APIs, paginated complete enumeration with programmatic counts, safe on-demand transcripts, and truthful busy/read-only state. Do not auto-submit to any pre-existing conversation or steal its live owner. Existing active sessions must remain protected; history discovery does not prove live control. Fresh isolated session for smoke only. No editing Hermes core/credential files.

SSH implementation:
- User authorizes SSH connection plus scoped automatic deployment of Astrorder's plugin to THEIR configured target. Current saved target comes from actual configuration; read only explicitly non-secret SSH settings via API/DB. Screenshot appears to use alias WSL but do not trust OCR over saved settings. Do not guess host/path or use placeholder examples as literal paths. Honor ssh config/agent and host-key checking; never read/print keys, store passwords, use StrictHostKeyChecking=no, silently accept unknown/changed host keys, request root/sudo, or alter unrelated remote services. Report actual SSH auth/host errors with UI remediation.
- Probe actual remote runtime, install/version-check only our plugin idempotently into target user's profile using supported plugin mechanism; create/start only our owned native bridge, through SSH stdio or loopback tunnel. No generic one-shot CLI chat/ACP/terminal text scraping substitute. No unauthenticated listeners, tokens in URLs/command lines/logs, user-input command injection. SSH remote command quoting is a separate boundary even with shell=False locally. Use fixed remote bootstrap with validated data passed on stdin, structured results. Validate remote host OS rather than assuming Windows or Linux from client.
- Connect workflow: SSH transport -> remote runtime discovery -> plugin deployment -> bridge start -> connector handshake -> session discovery. Connected only after actual native handshake; ssh -G is config validation only. Deployment retry safe, owned processes and forwarded ports cleaned on failure/disconnect; no killing user's runtime.

Preview/frontend:
- Keep frontend 30001 and backend 30002. Never use 5173. Preserve latest sidebar click fix: removed custom navbar z-index:1 in frontend/src/index.css. Its red/green browser regression is frontend/e2e/navigation-hit-test.mjs; run it. Keep existing theme/layout.
- Distinguish connection display name and SSH config alias. Multiple connection list, add/edit/remove, source/project hierarchy, scoped search and working routing. Avoid fake empty results when API is paginated or fails.

Verification:
- Test-first focused cases: two simultaneous connections isolated, existing singleton migration, stable IDs across restart, multi-project same/different source cases, paginated existing sessions, failed deploy/host-key/auth failure cleanup, repeated deploy no damage, stale snapshots not online.
- Real browser clicks on desktop/mobile and current preview, not only direct goto screenshot. SSH actual configured host attempt now authorized as above. If environment fails authentication or runtime prerequisite, show exact safe blocker; code should still implement path, not unconditionally reject.
- Native smoke only new isolated session; no prompt injection into pre-existing user conversations. Preserve identities/unknown no-resend semantics.
- Run backend tests/Ruff, frontend test/lint/build, existing regression and new browser tests. Read back effects after remote deployment and current preview connection/project/session states.
- No commit/push unless new user request; no credential printing, no core edits, no global model changes, no Desktop/Overlook restart. Restart only owned Astrorder preview if necessary after checking current processes. Do not assume stale process handles.
- Same exact error 3 failures: stop that avenue and record; no infinite loops. Do useful independent work instead.

Deliver docs/reports/ssh-multi-projects.md with actual changed files, test results, actual remote/native evidence and source session counts, current preview URLs and remaining limitations. Tasks are NOT complete merely because fixtures pass or CLI exits 0. Save checkpoint results if budget expires; do not present scaffold as implemented SSH.
