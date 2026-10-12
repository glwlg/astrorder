# Independent P2 control/EventStream review

Result: PASS (scoped)

No substantiated high- or medium-severity logic defect remains in the final hashed DaemonControl/EventStream implementation. This is not a claim that all P2 services or stage exit criteria are complete.

Repository identity

- Repository: `P:/workspace/glwlg/ai/astrorder`
- Branch: `master`
- Commit: `ff32f947024b1840b939ebfb8e58e45886d34b47`
- Source basis: uncommitted working tree; the per-file hashes below are authoritative for this review.
- Reviewer mutations: none in the repository. Only this report and its JSON companion were written.

Findings

No current high-, medium-, or low-severity findings.

Reviewed contracts

1. Bounded stream sends and shutdown completion: pass.

`internal/grpcapi/events.go:301` starts at most one transport `Send` per subscription and selects on send completion, stream cancellation, or the server timer. Returning the RPC handler cancels the gRPC stream context. The real TCP/HTTP2 slow-reader test deliberately never calls `Recv` and checks that both the subscription and sender leave without client cancellation or daemon shutdown. This closes the original defect where timeout was checked only after synchronous `Send` returned.

`internal/grpcapi/server.go:72` marks acceptance in the interceptor but signals shutdown only from server `stats.End`, after response serialization/status submission. A transport error releases the provisional shutdown gate. This is an accepted-transport-response completion gate; it does not claim guaranteed remote client receipt.

`cmd/astrorder-sessiond/shutdown.go:12` bounds `GracefulStop` and calls `Stop` after the deadline. `main.go:95` waits for `shutdownDone` before the process exits.

2. Snapshot/replay/live handoff: pass.

`internal/events/subscription.go:21` takes `commitGate` exclusively, registers the subscriber, and obtains durable session high-water marks while commits cannot cross the snapshot boundary. `internal/grpcapi/events.go:88` binds the requested epoch, replays only through each captured `MaxSeqID`, emits `LiveReady` with those watermarks, then drains queued post-barrier frames. Tests cover paged replay across more than 128 sessions, a new session and new event during replay, expired/ahead cursors, epoch mismatch, and bounded subscription overflow.

3. Authority and runtime ownership safety: pass.

`internal/protocol/facade.go:69` fails closed when durable event or operation authority cannot be read. `facade.go:134`, `internal/protocol/admission.go`, and `internal/runtime/shutdown.go` close daemon/runtime admission before the final shutdown checks. Unconfirmed shutdown is refused for pending/uncertain durable operations, registered runtime activity, non-idle or ambiguous sessions, incomplete authority, or an epoch mismatch. Failed authorization or response transport rolls the provisional gate back; accepted shutdown keeps it closed.

4. Native and unknown payload preservation: pass.

`contracts/proto/astrorder/sessiond/v1/sessiond.proto:835` carries `native_payload` alongside the typed oneof. `internal/grpcapi/events.go:229` serializes the complete stored payload into that field for every event, including typed `runtime.owner`, `runtime.ownership_lost`, and `pty.output` branches. Incoming native JSON uses `json.Decoder.UseNumber`. `native_payload_test.go` covers nested unknown fields and integers beyond JavaScript's exact range.

5. Authentication and service surface: pass.

Unary and stream interceptors share bearer verification using `subtle.ConstantTimeCompare`. The current server registers and advertises only the two implemented services, `DaemonControl` and `EventStream`; this review does not treat the remaining P2 services as complete.

Defects resolved during the review

The working tree changed concurrently. The final hashes include fixes for four defects observed in earlier versions:

- Blocked stream send was not actually bounded. Final code uses a timer/context-select send path, covered by fake blocked-stream and real TCP slow-reader tests.
- Typed event branches dropped unmodeled native fields. Final contract/code carries complete `native_payload`, covered by exact-number/unknown-field tests.
- Shutdown authorization was a point-in-time check that allowed new runtime dispatch before process shutdown. Final code closes daemon and registry admission before authority checks.
- A failed legacy WebSocket shutdown-response write could leave the live daemon permanently draining. Final code calls `CancelShutdown` on write failure, matching the gRPC transport-error rollback.

Verification

- `go test ./internal/runtime ./internal/protocol ./internal/grpcapi -count=1 -timeout=120s`: runtime and grpcapi passed; protocol reproduced the shutdown-admission race. The final hash set contains the subsequent admission fix.
- `go test ./internal/runtime -run TestShutdownAdmissionRejectsNewWorkAndCanBeReleased -count=1 -timeout=30s`: passed.
- Final later run of `go test ./cmd/astrorder-sessiond ./internal/grpcapi ./internal/protocol -count=1 -timeout=120s`: command and protocol packages passed. The grpcapi package build was blocked by a concurrently added, out-of-scope `operation_test.go` that referenced `RegisterOperationQuery` before that implementation landed.
- A clean final `go vet` rerun was likewise not available against a stable operation-query tree. Coordinator claims of a green broader run were not counted as independent evidence.
- No WSL race run was duplicated. No production service, kernel, real database, secret, or chat system was accessed.

The verification status is therefore partial because of concurrent uncommitted activity, despite the scoped logic review passing. The green package results are not presented as proof of full P2 completion.

Suggestions (non-findings)

- Once concurrent OperationQuery work settles, rerun `go test` and `go vet` for `./cmd/astrorder-sessiond`, `./internal/grpcapi`, and `./internal/protocol` against the exact final hashes.
- Add a deterministic legacy WebSocket shutdown response-write failure test to lock in rollback symmetry with the gRPC `stats.End` error path.
- Retain the real slow TCP-reader test; the fake stream test alone does not prove that handler return cancels a blocked transport send.

Final reviewed SHA-256 set

- `contracts/proto/astrorder/sessiond/v1/sessiond.proto` — `c484afa2eebd3343d1aea602a66295154c3501bef5efdfeef340d546a1064fb0`
- `contracts/gen/go/astrorder/sessiond/v1/sessiond.pb.go` — `04bea259d89b78774a46b81b03fe938d41e8d7f2127f0c3866f28ebbbf0b0acc`
- `session-daemon-go/internal/grpcapi/server.go` — `0d2032aa7abc1268fcf8a07a09b95649e60ec8bc5daacb6c7a85168ef91226dc`
- `session-daemon-go/internal/grpcapi/control.go` — `0041314eac6233c658fd088ffe4c14f962a7157f582cac48d94629fedf2a39af`
- `session-daemon-go/internal/grpcapi/events.go` — `58a198dd3c78adfb8f6bb3d63fe87362ec5fded2995013a5bd2fb3a39f64b700`
- `session-daemon-go/internal/grpcapi/blocked_send_test.go` — `5a4f3c39b2509dc7001249de124a91f6181bbfa5ef8ed4051294d6ad4afd4806`
- `session-daemon-go/internal/grpcapi/slow_reader_test.go` — `f2fea3200f4b6ec52527e7433e856aa980a18f86bbad54100b81cede6ff6e094`
- `session-daemon-go/internal/grpcapi/native_payload_test.go` — `51c42524b4eec2fd1ae877e4bc3652643db75ecdd3327301203d819c46367b6f`
- `session-daemon-go/internal/grpcapi/shutdown_completion_test.go` — `1b528613c512cdf5802acada5164e2ee02d121ddd525a9e855a8cb85383e7288`
- `session-daemon-go/internal/grpcapi/shutdown_order_test.go` — `8a90bdfce067456908120ae4ad061b2e579f7296f57513df530a2411a61214ff`
- `session-daemon-go/internal/protocol/facade.go` — `ab765ad72e5060ff4c3ed794769f837c227bb33c60a47fd5db6b30932a6ef78e`
- `session-daemon-go/internal/protocol/admission.go` — `836a30e5e128a64b432b37e5887c82287950a61c542d719ac8ab8cd3f342dc1e`
- `session-daemon-go/internal/protocol/daemon.go` — `4542da8bb5ff714869f73fecaf2a57e517f44f5b7973b7acb1023820bd7ea5dd`
- `session-daemon-go/internal/protocol/live.go` — `9dc6c7dee4b2b91569793c6c55a4d7261e6ca12434d5762f54a2d249a6774ebb`
- `session-daemon-go/internal/events/subscription.go` — `06f06404232923732b5c79d25280206dcb4b060a55ca6e596ed113b5867dca0a`
- `session-daemon-go/internal/events/commit.go` — `da9770f27ac8a8db0de77418e3428ffd9f31c0065f38860ee4f6077d2d54e134`
- `session-daemon-go/internal/events/journal.go` — `166641fa3f0da5fda26cc77bfc9e14f495ed73c49112d66ceec44cacb8b630ec`
- `session-daemon-go/internal/runtime/registry.go` — `ffa890ef89df5f69f7c7eba886dc79cb0aff05c137aeb497472ea27240f68970`
- `session-daemon-go/internal/runtime/control.go` — `28a0c0fadd1c5bb57a6023dc8089d81342f6e5836bd0f30b36e2d256fc6106af`
- `session-daemon-go/internal/runtime/reload.go` — `837d9c4ad65bcab483ce1a89fc65cca9acec245e85dcf694aa05544f5fea10d1`
- `session-daemon-go/internal/runtime/shutdown.go` — `dfc2cd42bbe1898ec870c285d98a407ac80f841259eaaab4678cf459ecbf32aa`
- `session-daemon-go/cmd/astrorder-sessiond/main.go` — `be1a2bb98dde87eb8ce991b8521b30e201781253afdda5d4c70d0a6e09ed32b1`
- `session-daemon-go/cmd/astrorder-sessiond/shutdown.go` — `8a7e7c046073e827ec5f60c256a50cb3e4379149e04fcab40cc5d9d466a10753`
