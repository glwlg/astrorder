# P2 RuntimeQuery.QueryNative isolation record

Status: REVIEWED (QueryNative only); independent JSON/Markdown review archived as `full-go-p2-native-query-review.json/md`, all 11 source hashes match the current scope. P2 and overall migration remain incomplete. Production services and Session Daemon were not changed.

Implemented bounded RPC `astrorder.sessiond.v1.RuntimeQuery/QueryNative` on the existing daemon. All 17 protobuf query selectors map through a closed method table to existing `Registry.Query` and native runtime adapters. Local Codex/Hermes/Grok and three SSH runtime kinds retain the existing admission and remote child registry. Legacy WS and gRPC share the same timeout, registry call and model-reload wake path; no second kernel or conversation creation path was introduced.

Input validation rejects wrong runtime family, unknown protobuf fields, malformed or trailing JSON, non-object options, unset nullable state, collisions with typed-owned parameters, and aggregate payloads above the 16,000,000-byte business limit. Options and top-level payload use `UseNumber`. Optional false/zero/empty and explicit null retain their distinctions. Responses retain unknown native result fields and clone the validated target.

Actual RED failures preserved: initial TCP `unknown service RuntimeQuery`; response target missing; drain error returned Unavailable instead of FailedPrecondition. Each was corrected and the focused tests passed. Initial whole-daemon tests failed because the pre-existing capabilities regression expected only three services; it now expects the fourth registration and retains the old assertions. The first Flash implementation worker exited 1 after investigation, wrote no code, and is not counted as implementation evidence.

Verification:

- Windows whole daemon `go test ./... -count=1 -timeout=120s`, `go vet ./...`, candidate build: exit 0.
- WSL Linux `go test -race ./internal/grpcapi ./internal/protocol ./internal/runtime -count=1 -timeout=120s`: exit 0 (19.700s / 10.730s / 1.071s), before the later test-only strengthening of the cancellation authority assertion.
- After that test strengthening, Windows grpcapi/protocol entire packages and vet: exit 0 (5.341s / 4.160s); the cancellation test also passed count=3. Linux race on the strengthened cancellation test passed count=3 (1.856s).
- Actual authenticated TCP routing tests cover 17 selectors and three SSH kinds using synthetic registered adapters; malformed requests do not dispatch. Cancellation reaches the native query context, rejects idle shutdown while in flight and releases shared admission; same connection remains reusable.
- Generated Python client -> real Go daemon executable -> existing Go Codex adapter -> synthetic local JSON-RPC child process: six assertions passed. Client disconnect/reconnect preserves the same native catalog PID. No session/operation is created, typed-field collision is rejected before native I/O, large integer/false/null extensions survive, private daemon exits 0, synthetic child confirms stdin EOF and the TCP listener is released. Original result copied byte-exact to `full-go-p2-native-query-process-result.json`.
- Source-inventory preservation verifier exit 0; frozen baseline raw SHA256 remains `c19a3dda9689fa886a17ba21fc29a0a9f46ee82fc8180235f8b719d6106d4ff0`.

Candidate `astrorder-sessiond-native-query.exe` SHA256: `fe900a6ac664de87f7681503ad3b67903001d365966ccbd3207481b2c6f25033`. Source is an uncommitted working tree; executable hash and HEAD do not independently certify current-source freshness.

Boundaries: Only QueryNative is implemented within RuntimeQuery. Other RuntimeQuery RPCs return Unimplemented. SSH options, config aliases and custom SSH executable return Unimplemented because the existing factory does not consume those fields. Native initialization retains existing runtime behavior; no new handshake configuration semantics are claimed. Synthetic native/SSH fixtures do not prove real Agent inference or complete remote runtime parity. Python App and Go App consumers, remaining P2 RPCs, T01-T08 total acceptance, legacy WS removal and production verification remain outstanding.
