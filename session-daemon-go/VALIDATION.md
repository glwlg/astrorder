# Go Session Daemon validation checkpoint

This document records the production cutover and earlier isolated validation checkpoints. Cutover does not imply full native acceptance.

## Production cutover — 2026-10-08

### Agent connection replay/reconnect fix (App-only deployment)

- Fixed the App replay barrier caused by native `hermes.command_complete` notifications containing `status=complete` and `persisted_turn`, but no App `command_id`. These notifications no longer block subsequent connector handshakes or fabricate an App command receipt. Explicitly empty/unknown command identities still fail validation.
- SSH Hermes startup now handles the exact `hermes adapter is closed` failure by requesting the existing daemon `runtime.disconnect` safety check for that connection, then spawning once after confirmed release. Active/unconfirmed sessions and unknown timeout failures are not retried or bypassed.
- Failing projection/reconnect regressions reproduced the defects; final affected Python bridge, paging, connector, completion, SSH and delivery suites: **44 passed in 4.82s**.
- Deployed only `hermes_projection.py` and SSH `control.py`; backups `hermes-connection-projection-20261008-172652` and `hermes-ssh-reconnect-20261008-173606`. No daemon replacement or restart: PID **50896** and epoch `86ca8e5f112af6041f05bf2d6ef78c6a` remained unchanged.
- Restored WSL Grok through its normal connect API. Subsequent App-only restart (App PID **38360**) automatically restored **9/9** local/Debian/WSL Hermes/Codex/Grok connections. Authenticated GET readback explicitly asserted all nine connected.
- Production daemon handshake sequences **23/18/13** matched App checkpoints for the three Hermes controls. The previously blocking completion stream reached checkpoint **3**. Production Hermes model APIs returned HTTP **200**, with local **25**, Debian **33**, WSL **49** items, both before and after the final App restart.
- Evidence: `agent-connections-verified.json` and `connections-restart-check.json` in Hermes scratch. Four historical pending/uncertain operations and the existing error session remain; no failed user prompt was resent. This verifies native control/handshake, replay consumption, App API state and restart restoration, not new inference or browser interaction.

### Long-message IPC regression fix (deployed; connection state below is historical)

- WSL Codex command `412326a7-a67d-43a7-ae05-89ed21d0b020` failed before operation admission. Its prompt was 47,733 UTF-8 bytes; the daemon had no corresponding operation or turn-start event.
- A read-only production `daemon.status` request with equivalent padding reproduced close code 1009: `read limited at 32769 bytes`. Go's WebSocket default was 32,768 bytes; Python's existing limit was 16,000,000 bytes.
- Both IPC and Connector WebSocket accepts now set the original 16,000,000-byte limit. Real socket regressions failed before their respective fixes and then passed; the send test checks exact runtime input and a completed durable receipt.
- Go full suite: 275 passed across 16 packages; vet and candidate build passed.
- Real WSL Codex integration: 1 passed, 1 local case deselected, 62.84 seconds. An isolated candidate daemon received a long prompt, native terminal execution wrote/read exact marker bytes, the assistant replied with the expected marker, App projection replay restored tool/assistant messages without duplication, and native history contained the entire exact prompt. The test deleted its session and temporary workspace. This exercises the App bridge/projection and native runtime, not browser input or production deployment.
- User-authorized restart deployed `go-hermes-20261008-170228/astrorder-sessiond.exe`, SHA-256 `5963b61d93b6e7850769fc6fa3d7149cb1a18b04f01bb6515963e665087259f4`, daemon PID 50896; checkpoint `large-message-20261008-170228`. App PID is 20044 after a subsequent App-only restart; daemon PID remained unchanged. The exact production executable selection and process path were verified, and the formerly failing long read-only IPC request now succeeds.
- Post-release local/Debian/WSL Codex and Grok connections are connected. Hermes recovery is not fully healthy: local/Debian report connecting (model catalog APIs return 200), while WSL reports error, its catalog returns 503, and an explicit connect retry returns 503. Do not report all nine connections healthy. Four historical pending/uncertain operations and the existing error session were retained; no failed user prompt was resent.

### Large-history Hermes model readback fix

- User-authorized restart deployed `go-hermes-20261008-163804/astrorder-sessiond.exe`, SHA-256 `ec38a6b2db5ccc6f7577fddd6b81fbea0db4f2ebdb294d3419c77226a171e9d2`, PID 53392; checkpoint `hermes-readback-20261008-163804`.
- Model reads now request `omit_messages=true`. The previous binary failed on the real Debian session `20261002_135958_6abd78` with `hermes process connection closed`; the candidate completed three repeated reads. Go 273 tests, vet/build passed.
- Production session spawn/model read succeeded, and the installed App's model-binding restoration function completed twice without sending a message. Catalog APIs returned local 25, Debian 33, WSL 49 items. Native lazy readback still reported an empty provider and a qualified tiered model differing from the saved selection; passing the restoration function is not proof of effective model equality or successful inference. Four historical pending/uncertain operations were retained during the authorized restart.

### Remote Hermes model catalog fix

- Released `%LOCALAPPDATA%/Astrorder/bin/go-hermes-20261008-155737/astrorder-sessiond.exe`, SHA-256 `80f6923970774356192edfa761c6d076ec350b524d8bd76b303165e150004db9`, daemon PID 17152. Backup: `%LOCALAPPDATA%/Astrorder/backups/hermes-models-20261008-155737`. User explicitly authorized restarting despite the uncertain error session.
- SSH multiplex snapshots now retain owner-scoped native identity and activity after read controls. The App SSH model catalog uses the runtime control connection, so listing models does not require resuming an unpersisted draft.
- Candidate Go suite: 272 passed; vet/build passed. Final affected Python controller/bridge/disconnect tests: 6 passed. Isolated native remote catalog reads returned Debian 33 and WSL 49 items on each of three repeated requests.
- Authenticated production model APIs returned HTTP 200: local 25, Debian 33, WSL 49 items. Following the focused App controller deployment and App-only restart, daemon PID remained 17152 and all nine environment Agent connections reached connected. These are catalog/connection checks, not new inference or tool execution acceptance.

### Hermes connection fix released later the same day

- Production candidate: `%LOCALAPPDATA%/Astrorder/bin/go-hermes-20261008-152603/astrorder-sessiond.exe`, SHA-256 `52ad17789b9966cb228ecb5546caa06275868bb76d137739e42c428e8676b484`; daemon PID 54204. Backup: `%LOCALAPPDATA%/Astrorder/backups/hermes-connect-20261008-152603`.
- Hermes control spawn now opens the native gateway without resuming/creating a conversation, preserves Agent/source/connection identity, publishes readiness and exposes read-only discovery. Remote launch uses the installed Hermes launcher and resolves its executable when non-interactive SSH lacks the CLI PATH.
- Reattaching an existing Hermes control connection republishes readiness for an App whose replay checkpoint already consumed the initial hello. A failing protocol regression reproduced this App-restart defect before the fix.
- Windows full Go suite: 271 passed across 16 packages; vet/build passed. Python Hermes/SSH controller and Go bridge regressions: 12 passed. Isolated real local/Debian/WSL native handshake, App projection, remote discovery and graceful shutdown passed with the final candidate.
- Authenticated production queries confirmed all nine connections connected and native Hermes session.list succeeded on all three environments. Storage healthy and pending operations zero. Debian's saved Hermes connection choice was disabled; enabled through the normal environment connection API.
- These checks establish connection/discovery behavior, not fresh LLM inference, tool execution, or eight-hour endurance.

The following cutover details describe the earlier binary before this fix.

The user explicitly authorized switching after exiting Astrorder, accepting follow-up fixes in Go. Production now runs Go on port 30009 (PID 24388, epoch `86ca8e5f112af6041f05bf2d6ef78c6a`). The installed desktop and existing startup task use the configured Go executable through `daemon_service.daemon_launch`; Python remains available for deliberate rollback.

- Binary: `%LOCALAPPDATA%/Astrorder/bin/go-20261008-145254/astrorder-sessiond.exe`; SHA-256 `f23d0ad1173fa07c8f974545b288b6716f190f9489ec9cc867a9794f1fdb0444`.
- Activation checkpoint: `%LOCALAPPDATA%/Astrorder/backups/go-activation-20261008-145254` (production configuration, encrypted credentials, consistent App database, previous installed launcher). Earlier complete source checkpoint: `go-cutover-20261008-143307`.
- Windows Go full suite/vet/build passed; actual child-process shutdown regression first failed then passed. Previously shutdown acknowledged without exiting; the accepted wire reply now signals main to close owned resources and the listener.
- Matching Python launcher and bridge integration: 25 passed. Actual runtime configuration passed isolated startup/authenticated status/graceful process exit before production activation. Pending App commands: 0.
- App health passed. App-only restart changed PID 68552 to 41956 while Go PID and epoch stayed unchanged. Authenticated Go storage reports not degraded, pending operations 0; all seven runtime types registered.
- App reports Codex and Grok connected on local Windows, Debian and WSL after automatic restoration. This verifies connection state, not new inference turns.
- **Known live defect:** local Hermes remains offline; Debian/WSL Hermes connection attempts fail. App sends a `runtime_control` session.spawn, but Hermes Spawn treats it as native conversation resume. Remote launcher defaults and missing Hermes runtime Query support also require follow-up. No claim of complete 9-connection parity or measured startup-speed improvement.
- No production rollback or 8-hour endurance test was performed. The older open-gate lists below are historical, not newly imposed prerequisites for this user-authorized cutover.

## Current evidence: retention and durable operation receipts

This section supersedes the completion claims in the historical checkpoints below. It is not full runtime parity or production approval.

- Retention candidate: Windows full Go tests, vet and build passed. Debian full race suite and vet exited 0 (`debian-retention-final.log`, process `proc_f217f56e81bb`).
- Python full suite against `astrorder-sessiond-retention.exe`: **557 passed, 3 skipped, 2 warnings** (`backend-retention-suite.log`). Skips are not accepted runtime evidence.
- Real Chromium / Python App / Go / SSH Codex chain exited 0 (`native-browser-retention.log`). Assertions cover native execution while the isolated App is stopped, recovery in the same browser document, native approval, fork history/title, and native plus App deletion readback. Artifact directory: `astrorder-browser-native-mHJwv4` under the Hermes scratch directory.
- Strict remote lifecycle test passed after fixing the fixture: Codex deletion must succeed and the same native ID must reject resume; Hermes uses a temporary HOME, waits through conservative active-handle refusal, requires delete success and reads back the exact ID from its isolated SQLite database. Latest execution: `TestRealSSHRemoteAdaptersOnDebian`, 6.219s package result. This is lifecycle evidence, not Hermes inference/approval acceptance.
- The isolated Hermes fixture disables lazy dependency provisioning using the installed launcher's `HERMES_DISABLE_LAZY_INSTALLS=1`. A prior timed-out fixture directory was removed and absence verified. This does not establish cleanup of all earlier test sessions.
- Journal retention now has total/per-session logical byte limits and age, preserved sequence watermarks after pruning, prune-on-open, and durable status/page agreement. It does not impose a physical SQLite file cap or bound the operation ledger.
- Durable operation reservations/receipts and read-only lookup prevent blind redispatch after a lost reply. Uncertain native outcomes remain reserved and block unconfirmed shutdown. Native reconciliation, orphan recovery and ledger retention still require acceptance.

Open gates: full native capability/environment matrix (including Hermes/Grok/WSL and Windows Codex tool execution), fault boundaries, source/build evidence freeze, independent review, all-test-resource cleanup, and matching installation/service integration. Page performance is not established by journal latency. The user deferred 8-hour endurance, production switch and rollback; none was executed. Early Python tests imported the default App before collection isolation was added, so historical absence of production contact is not established.

## Remote SSH Multiplexing, Disconnect Action, and Desktop Stops Checkpoint (historical tree)

- Windows current tree: **212 passed in 16 packages**, `go vet ./...` exit 0, and `.runtime/astrorder-sessiond.exe` binary built cleanly.
- Debian current tree: full `-race` suite passed in isolated verification on `192.168.1.100`, including opt-in real Hermes native lifecycle tests (`TestRealHermesIsolatedGatewayOwnership`, `TestRealHermesCreateResumeHistoryRenameDelete`, `TestRealHermesPersistedSessionRecoversThroughRegistry`). Temporary build directory cleaned up immediately after verification.
- Python cross-language integration suite against real isolated Go daemon: **5 passed** (`test_go_daemon_bridge_integration.py`, `test_go_daemon_grok_integration.py`, `test_go_daemon_model_config_integration.py`, `test_go_daemon_pty_integration.py`, `test_go_daemon_disconnect_integration.py`).
- Integrated dynamic `MultiplexRegistry` for SSH runtimes (`codex-ssh`, `grok-ssh`, `ssh`) with SHA-256 canonical settings validation, per-connection child isolation, and targeted connection teardown.
- Implemented `runtime.disconnect` protocol action with authoritative active-session gate (`running`, `waiting_approval` reject disconnect to prevent orphaned active tasks) and durable state cleanup.
- Implemented real remote Hermes SSH starter in `internal/runtime/remote/hermes.go` using OpenSSH Transport, and added remote gateway simulation unit tests in `internal/runtime/hermes/remote_test.go`.
- Implemented background Codex Desktop Stops observer coroutine in `internal/runtime/codex/desktop.go` with context cancellation wired into `daemon.AddCloser`.
- Hardened `scripts/switch_to_go_daemon.py` with strict authoritative status parsing and mandatory secret validation to prevent blind switching.
- Implemented `StartDesktopStopObserver` monitoring `~/.codex/astrorder-observer/events/*.json` with regex stem and 5-minute age validation for real-time turn completion forwarding.
- Connector protocol WebSocket (`/ws/v1/connector`) now buffers incoming `hello` and `agent.upsert` events until an agent control session is bound (`BindAgentControlSession`), preventing missed startup frames.
- Production switch script `scripts/switch_to_go_daemon.py` hardened to prevent non-interactive restart or forced takeover while authoritative active sessions exist.

## Failed-worker recovery and model-config library checkpoint (earlier isolated tree)

- Windows current tree: **137 passed in 14 packages**, `go vet ./...` and executable build passed. Debian current tree: full `-race` suite passed **three repetitions**, vet and Linux build passed in `/tmp/astrorder-sessiond-verified`.
- Real Debian `TestRealHermesIsolatedGatewayOwnership`, `TestRealHermesCreateResumeHistoryRenameDelete` and `TestRealHermesPersistedSessionRecoversThroughRegistry` each passed **three repetitions**. All profiles/workspaces are disposable. The persistence fixture uses the real gateway's create-time messages path (no LLM generation), closes the seed gateway, resumes via Registry, terminates only that test-owned gateway, and resumes the exact stored session in another PID with history readback. This accepts that scoped persisted-native recovery path, NOT daemon restart continuity, inference/tool-loop resume or production takeover.
- Failed native worker replacement requires confirmed owned process exit/cleanup and completion of the retired projection dispatcher; blocked retired projections and in-flight controls reject recovery. Native resume failure leaves conservative error ownership. Registry Spawn reads owning native snapshots instead of returning cached idle, retries errors through the adapter, and rejects command/replace races. Circular slash aliases fail explicitly.
- Imported `internal/modelconfig` into the main Go module; **17 package tests passed**. Added complete TOML grammar parsing (`github.com/pelletier/go-toml/v2 v2.4.3`), managed Magpie catalog/BOM parity, exclusive random private staging, pre-cancelled apply rejection, cleanup of staged files, bounded backup retention, reported rollback failures and secure rollback staging. Missing native reload/activity callbacks fail rather than claiming reload success. The library is **not wired into daemon control routing**; Windows native credential storage, runtime reload callbacks, SSH targets, full contract compatibility and file-race/durability acceptance remain open.
- Fixed Python `_drain_config_reloads()` to defer an Agent's reload unless its owned session states are confirmed idle. The formerly failing active-session reload regression now passes. Expanded session-daemon/bridge/page/real-Go/real-PTY regression against the rebuilt Windows executable: **50 passed**. Targeted daemon Ruff F821/F841/E9 passed. Source changes do not imply a production Python reload.
- Independent source-fed recovery/model-config review returned findings, **not PASS**. Reproduced and fixed rollback error suppression; predictable staging and rollback paths now use exclusive private temporary files. Other findings require reconciliation: Go CreateTemp already guarantees 0600 before umask; uncertain native cleanup must retain quarantine rather than deleting ownership; snapshot authority and actual Windows path normalization must be judged against executable tests, not review labels. No final signoff is claimed.
- Scoped diff checks pass with `core.whitespace=cr-at-eol` for existing Windows line endings. Unrelated dirty frontend files were not modified by this work.
- No production daemon, existing gateway or pre-existing Agent was stopped, restarted or replaced. Complete SSH/Grok/model-config control integration, real generation/approval/attachments, daemon restart reconciliation, journal isolation/retention/performance and final review remain release gates.

## Hermes per-session gateway and owned-tree checkpoint (earlier isolated tree)

- Configuration now selects `hermes.NewIsolated`: one durable session per native gateway, bounded dispatch overflow isolated to that session, and no gateway launch during configuration registration.
- Windows full suite: **115 passed in 13 packages**, followed by vet and executable build. Debian full `-race` suite passed **three repetitions**, followed by vet and Linux build in `/tmp/astrorder-sessiond-verified`.
- Real Debian `TestRealHermesIsolatedGatewayOwnership` and the native lifecycle test each passed twice. The isolation test creates two distinct native processes, closes one, verifies the other's exact native title write/readback, and deletes the surviving disposable session only after authoritative idle. All homes/workspaces are temporary; no LLM prompt or existing profile was used.
- Native empty Hermes drafts are not stored until the first prompt on this installed gateway. A fresh process correctly rejects their resume; the test explicitly requires that rejection and no false idle ownership. This is not crash-recovery acceptance for persisted conversations.
- Added native-owned tree teardown: Windows suspended launch, Job assignment before resume, kill-on-close and active-process readback; Debian dedicated process groups and `waitid(WNOWAIT)` observation before group cleanup/reaping to avoid recycled-PID signaling. Actual descendant heartbeat tests and gateway child cleanup pass on both hosts.
- EOF invalidates handles, wakes pending RPCs, stops dispatch and retains conservative error status. Late completion cannot restore idle after transport failure. Native resume validates the returned workspace against the allowlist and waits for the exact native handle before accepting ownership. Deletion rejects concurrent in-flight controls without holding pool metadata locks across native IO.
- Source-fed independent review returned findings, not PASS. Added/fixed foreign approval ownership rejection, event/delete nil checks, synchronized interrupt handle reads, bounded close failure, and conservative error after failed submit. Final review signoff and safe failed-worker reconciliation remain open; no automated takeover is authorized by an error snapshot.
- Python bridge/replay/real-PTY suite: **24 passed** against the newly rebuilt Windows executable.
- No production service or pre-existing Agent was stopped, restarted or replaced. Complete SSH, model configuration, real generation/approval/attachment/history, persisted native recovery and performance acceptance remain release gates.

## Hermes native adapter checkpoint (earlier isolated tree)

- Integrated `internal/runtime/hermes` and configuration registration for the native TUI gateway. Gateway creation does not occur while registering configuration. The Windows history reader uses the same read-only SQLite implementation as Linux, rather than an unsupported-platform stub.
- Added failing-then-passing boundaries for workspace validation before launch, pre-cancelled launch, concurrent readiness, foreign notification filtering, structured stream projection, slow projection versus RPC responses, unknown/cross-session approvals, failed approval retention, read-operation status preservation, early completion before prompt acknowledgement, approval write readback, history limit validation, active-handle deletion protection, and concurrent resume singleflight.
- Windows final current tree: **104 passed in 13 packages**, vet and executable build passed. Debian final production-code tree: full race suite passed three repetitions, followed by vet and Linux build. The opt-in native lifecycle then passed **five consecutive runs** after awaiting authoritative idle before deletion; the guard continues rejecting transient/unknown statuses rather than treating them as safe.
- A real Debian native gateway passed `TestRealHermesCreateResumeHistoryRenameDelete`: an isolated temporary Hermes home and workspace, empty session create/resume, model catalog, empty history, title write/readback, delete and rejection of later resume. No LLM prompt was sent; this is not acceptance of live Agent generation, native approvals or attachments.
- Python bridge/replay/real-PTY regression remains **24 passed** against the rebuilt executable.
- Independent source-fed review returned findings, **not PASS**. Concurrent native resume was reproduced and fixed with a per-session context-aware permit. Shared-gateway overflow still fails all its owned sessions; per-session failure isolation, Windows owned process-tree cleanup, native EOF reconciliation and strict history paging still require acceptance. Claims of SQL connection leakage, numeric assertion panic and shell command injection were not established by the review: the code closes the DB, opens mode=ro, uses non-panicking assertions and dispatches JSON RPC rather than a shell. Broader path/ID hygiene remains to be verified.
- The previous Debian PTY timeout did not recur with explicit `/bin/sh`: the earlier full race suite passed three repetitions. This validates deterministic test configuration, not proof of a default-shell runtime root cause.
- No production daemon, gateway or pre-existing Agent was restarted or replaced. Hermes/SSH/model-config/native recovery/performance parity is still incomplete; production switching is prohibited by this checkpoint.

## Native runtime/control and PTY checkpoint

- Windows current tree: `go test ./... -count=1 -timeout 90s` reported **75 passed in 12 packages**; `go vet ./...` and the Windows executable build passed.
- Debian current tree: full `go test -race ./... -count=1 -timeout 120s`, vet and Linux build passed in `/tmp/astrorder-sessiond-verified`. The directory is isolated; existing services were not restarted.
- Current Python bridge/page-validation/real-process suite: **24 passed**, including the new `tests/test_go_daemon_pty_integration.py`; Ruff F821/F841/E9 passed for that new test.
- Real PTY controls were exercised on both Windows ConPTY and Debian: spawn, disconnect/reconnect, actual shell output replay/live delivery, resize, and explicit close. Markers are assembled by the shell from separate fragments, so an echoed command cannot masquerade as execution.
- Fixed startup reservations so slow native terminal creation runs outside the runtime metadata lock; a late factory return after Close is cleaned up rather than registered.
- ConPTY now sets STARTF_USESTDHANDLES; without this, real child output inherited the parent streams instead of the pseudoconsole. Explicit UTF-16 environment blocks, executable paths with spaces, Unicode environment values, native exit monitoring, and handle cleanup have regression coverage. The handle-valued pseudoconsole attribute uses the native uintptr ABI rather than an invalid Go unsafe.Pointer cast.
- Fixed integer/range validation for resize, split-UTF8 projection across JSON frames, durable emission failures, and native cleanup error reporting. Explicit close drains the final callback before releasing the runtime binding, allowing safe sequential reuse of that ID.
- Codex, Grok and PTY native launch environments now omit ASTRORDER_SESSION_DAEMON_SECRET; provider/runtime variables are preserved. This does not establish OS-level sandbox isolation.
- Configuration registration and the current control/runtime packages are included in the passing full suite. This is not proof of full native action parity.
- Independent PTY review is **not signed off**. The first filesystem-based reviewer was blocked by the Windows sandbox; a source-fed reviewer returned findings. The explicit-close binding issue was reproduced and fixed. Other findings require reconciliation against locking, EOF cleanup and regression evidence; do not report review PASS or use this checkpoint to authorize production rollout.

## Earlier replay-only checkpoint (before runtime/control expansion)

- Windows: `go test ./... -count=1 -timeout 60s` passed (41 tests reported by the runner); `go vet ./...` and executable build passed.
- Debian: `go test -race ./... -count=1 -timeout 90s`, `go vet ./...`, and Linux executable build passed in `/tmp/astrorder-sessiond-test`.
- Debian native Codex: `TestRealCodexCreateResumeAndDelete` passed using `/home/luwei/.vite-plus/bin/codex`. It creates a disposable empty thread, closes the creator, resumes with a new transport, deletes the thread, and rejects subsequent commands locally.
- Regression coverage includes ordered notifications, explicit permission grants/decline schema, unsupported server request errors, native approval-resolution and turn-completion projection, active deletion rejection, and symlink allowlist escape rejection.

## Implementation changes

Notification delivery uses a bounded sequential worker instead of a goroutine per frame; overflow fails pending requests and closes the isolated transport. Stdin writes no longer hold the shared state lock. Workspace checks resolve links and honor POSIX case sensitivity. RPC error payloads are retained and non-object results are not treated as rejection. Close sends EOF and waits briefly before killing the owned process. Native resolution notifications reconcile approval state. Approval decisions are serialized to prevent duplicate replies. EOF invalidates the transport and marks the runtime as errored; future requests fail immediately. Foreign-thread notifications cannot mutate an owned session. Close wakes pending RPC waiters immediately. Four regression tests cover these boundaries; final Debian Codex race tests and vet passed after adding the close-wakeup test.

## Multi-session replay checkpoint

- Go status advertises `replay.batch_limit=128` and `replay.explicit_handoff=true`. The Python bridge splits session checkpoints only when explicit handoff is advertised, defers live delivery across all batches, then sends an empty sync to release the stream. Legacy Python daemon replies keep their original path.
- `daemon.status` with `prepare_replay=true` registers the authenticated subscription before the status snapshot, closing the status/first-sync gap for newly created sessions.
- Validate page metadata, final boundaries, boolean flags, missing known sessions and contiguous sequences before projecting a page. Malformed pages do not advance the checkpoint; previously accepted valid pages retain their checkpoints.
- Python bridge/validation/integration suite: 23 passed. Run with `ASTRORDER_GO_SESSIOND` pointing to the freshly built binary to enable `tests/test_go_daemon_bridge_integration.py`.
- Real Windows Go-process/Python-bridge integration: 130 initial sessions plus a session with 1005 committed frames; 1135 replayed frames, publication during replay, a new session's two frames in the status/sync gap, and restart of the isolated idle daemon with zero repeated projections. This is event/bridge acceptance, not native Agent execution or process adoption.
- Independent read-only review passed for batching and handoff, then passed for the prepare-replay increment; no security concerns or logic errors reported. Review session: `ssh-codex-ssh-1a32825b625d4eb4a7b729d5::01a0f2cd-fa02-7322-9a23-69bc87309e84`.
- Expanded Python regression including `tests/test_session_daemon.py`: **48 passed, 1 failed**. The failure `test_model_config_reload_waits_for_active_agent_sessions` reproduces alone; its test and Python daemon source are unchanged in this worktree. `_drain_config_reloads()` currently reloads without checking active sessions. This existing acceptance gap remains open; the expanded suite is not green.
- Targeted Ruff F821/F841/E9 checks passed. Git diff whitespace check passed using `core.whitespace=cr-at-eol` for the existing Windows line endings.

## Remaining acceptance gaps

- Runtime integration with daemon control routing remains incomplete. Durable Commit now writes SQLite before broadcasting, retains daemon identity and restores recorded session statuses; native process reconciliation and production replay transport are not accepted yet.
- Durable storage currently serializes commits under the journal lock. Per-session writer isolation and retention still need implementation and performance acceptance. Bounded replay pages now expose fixed Through/Next cursors and durable low watermarks. session.sync preserves the existing envelope and adds has_more/next_seq_id plus optional through and limit inputs; App Server continuation handling now drains replay pages at the fixed boundary, checks cursor progress and frame agreement, and preserves legacy single-page replies (13 bridge tests passed). Canonical numeric timestamps now persist through SQLite migration, replay and sync; legacy records retain timestamp 0 because their original timestamp is unavailable. Live subscription now registers before sync, buffers through pagination, suppresses frames already included in replay, uses a single socket writer with a 2-second write deadline, and disconnects unhealthy subscriptions. Three real WebSocket regression tests pass. Multi-session batching and explicit handoff now pass unit and real cross-language tests; sustained-load budgets and complete protocol parity remain unaccepted.
- The executable requires ASTRORDER_SESSION_DAEMON_DB and uses protocol.Open; invalid storage startup fails explicitly.
- Unsupported server requests receive explicit errors; dynamic tools, elicitation, authentication refresh and other native request types are not implemented.
- Permission approval payload validation beyond explicit JSON permissions remains incomplete.
- Close callback completion, blocked callback handling, late notifications, process crash recovery and concurrent command/delete state transitions need further coverage.
- Native turn execution, real approvals, attachments, models, SSH runtime integration and full history parity have not been accepted.
- Empty-thread recovery has passed on this Debian installation; behavior in clean Codex homes and across versions still requires verification.
- Windows race tests remain unavailable without a C compiler; Debian race coverage is verified.

No production daemon was replaced or restarted by this checkpoint.
