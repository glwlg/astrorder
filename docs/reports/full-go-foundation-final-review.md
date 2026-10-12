# Full-Go foundation final review

Decision: PASS for the bounded foundation tranche only.

`passed` is true and the issue array is empty. This result accepts the reviewed six CLI lifecycle fixes, the post-fix Store/event foundation (including the latest attachment transaction correction), and the isolated P4 native-route/private-proxy source contract. It does not declare P9 done, the full migration done, or any binary released, installed, deployed, or production-complete.

Repository snapshot

- Repository: `P:/workspace/glwlg/ai/astrorder`
- Branch: `master`
- HEAD and `origin/master`: `ff32f947024b1840b939ebfb8e58e45886d34b47`
- The reviewed foundation is untracked worktree content. The SHA-256 map in the JSON report is therefore the authoritative reviewed snapshot.
- No source file was edited by this reviewer. Only this Markdown report and its JSON companion were written.

Findings

No blocking security or logic issue remained in the bounded tranche.

Lifecycle P9-CLI-001..006

- P9-CLI-001 passed. Provisioning resolves and confines the final native paths for the installation root, executable, static tree, state/data/attachments directories, and state-file destinations before mutation. The Windows junction-escape test passed.
- P9-CLI-002 passed. Lifecycle health and shutdown use a bounded isolated client with `Proxy: nil`, refuse redirects, and reject malformed or trailing JSON. The redirect tests include the bearer-bearing shutdown request.
- P9-CLI-003 passed. Protected/public `ASTRORDER_*` keys require canonical casing and Windows child-environment construction is case-normalized and deduplicated. Mixed-case rejection occurs before writes.
- P9-CLI-004 passed. Re-provisioning repairs private state/config/credential DACLs to current-user plus LocalSystem without changing file bytes.
- P9-CLI-005 passed. Canonical final state identity drives the Windows mutex, alias paths converge, and restart holds one lock across stop/start.
- P9-CLI-006 passed. The real-process test registers credential restoration before deliberately corrupting the fixture.

The opt-in `TestAppCLIRealProcessLifecycle` ran against the supplied private candidate (`7462c532b2b7b49731ecbd92615ee70d900aee1abd13882aeec4a9a9ca965b8e`) and passed provision, initial status, start, duplicate start, status with corrupted encrypted credential fixture, restart, stop, listener release, and independent-sentinel survival without lifecycle requests.

Store/event post-fix review

- Python canonical hash compatibility passed for sorted Unicode, `<`, `>`, `&`, U+2028/U+2029, a very large integer lexeme, and the `1` versus `1.0` distinction. Independent Python probes produced `a97d9d…` and `613258…`, matching the Go fixtures.
- `codec.go` changed during this review from `a549fb…` to `f295a2…` to distinguish literal `\\u2028`/`\\u2029` text from actual separators. The current source was repinned. Three independent Python hashes (`e646ca…`, `289630…`, `1ac59d…`) matched the new Go regression test; the full Store suite, focused tests, and vet were rerun and passed.
- Code-point limits preserve valid UTF-8 on session/task/history paths.
- Current-version validation rejects structural table/default/primary-key/unique/index drift. Old-version canonical rebuild runs inside the migration transaction. Windows `LockFileEx` and Unix `flock` provide cross-process migration ownership; the two-process test passed.
- Projector identity is an opaque typed handle owned by one Store. The closure is retained directly, and registration does not depend on Go function layout or `uintptr` identity.
- `ApplyFrame` keeps projection mutations, durable events, and the checkpoint in one transaction. Projection failure returns no committed in-memory events and rolls back data/events/checkpoint; duplicate current sequence is idempotent and non-contiguous input is rejected.
- `AddAttachment` now performs insert, readback, and wire conversion inside `WithTx`. `TestAttachmentReadbackFailureRollsBackInsert` passed and proves a wire-conversion failure leaves no committed row.
- Blob ownership semantics are consistent: a renamed blob is removed if metadata persistence fails; once `AddAttachment` commits, the row owns the blob, so event-publication failure preserves both rather than leaving a row that points to a deleted file. The focused handler tests and Store suite passed.

P4 source-contract review

The harness was run with the explicitly requested executables:

- Node: `C:/Users/luwei/AppData/Local/hermes/tools/node-26.7.0-win32-x64/node.exe`
- Candidate: `C:/Users/luwei/AppData/Local/hermes/cache/scratch/astrorder-app-p4-real-source.exe`
- Python: `P:/workspace/glwlg/ai/astrorder/backend/.venv/Scripts/python.exe`

The fresh run passed all assertions:

- real Python bootstrap authentication and cancellation routes;
- real attachment write/read/size limit;
- real Store durable replay plus EventHub live delivery;
- compiled frontend bootstrap in headless Chromium;
- authenticated App-only shutdown, listener release, WebSocket closure, and continued source health;
- fixture-only streaming, private-header sanitization, and upload cancellation propagation;
- all owned source/App processes exited with code 0 and all listeners were released.

The signal is honestly labeled. `p4_contract_upstream.py` constructs real Python App routes and private Store/AttachmentManager/EventHub/task components with lifespan disabled. It explicitly runs no daemon, native Agent, observer, automatic connection, chat, or inference. `/__fixture__/*` assertions test proxy mechanics and cleanup; they are not presented as Agent inference evidence. The fresh transient state directory was removed after its result and audit were extracted and hashed.

Verification

All commands below exited 0:

- `go test ./internal/lifecycle ./cmd/astrorderctl ./internal/credentials -count=1 -timeout=300s`
- opt-in `go test ./cmd/astrorderctl -run '^TestAppCLIRealProcessLifecycle$' -count=1 -v -timeout=90s` with the supplied candidate and hostile inherited `ASTRORDER_*` fixtures
- focused lifecycle junction/redirect/trailing-JSON/mixed-case/DACL/mutex tests
- `go test ./internal/store -count=1 -timeout=180s` before and after the detected `codec.go` change; final result `ok astrorder.dev/app-server/internal/store 2.679s`
- focused hash/Unicode/schema/cross-process/attachment/projector tests
- focused `ApplyFrame`/Projector/attachment transaction tests; final result `ok astrorder.dev/app-server/internal/store 0.561s`
- `go vet ./internal/store ./internal/lifecycle ./cmd/astrorderctl ./internal/credentials`, followed by Store vet after repinning
- independent Python canonical-hash probes
- the explicit-Node P4 source-contract harness
- transient harness cleanup verification

The companion JSON contains exact commands, exit results, evidence and source SHA-256 values.

Remaining stage gaps

- P8 remains a prerequisite. This review does not declare P9 or another migration stage complete.
- Final production composition still needs to demonstrate Go App Store opening, real projector registration, daemon gRPC projection, and event publication end-to-end.
- P4 evidence is an isolated source comparison, not production lifespan, daemon, Agent, inference, or real-user-data evidence.
- No production database, credentials, chat system, daemon, installation, service deployment, cutover, or user data was touched.
- The retained five-binary Go-only build predates the latest attachment and codec changes. A current rebuild/package verification is still required in a later release tranche.
- Coordinator-reported all-package tests/vet, contracts, and earlier build results were not promoted to release evidence by this bounded review.
- The accepted code is currently untracked worktree content, not a committed or released artifact.
