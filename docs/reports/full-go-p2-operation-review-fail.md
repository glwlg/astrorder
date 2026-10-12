Review result: passed: false.

One medium-severity logic defect remains. No critical/high findings were found. The default three-service registration, capability advertisement, authentication, exact scope lookup, unknown-field rejection, restart persistence, and no-redispatch behavior passed review and tests.

Finding

MEDIUM — malformed completed receipts lose their preserved native bytes

Path:
session-daemon-go/internal/grpcapi/operation.go:54
session-daemon-go/internal/grpcapi/operation.go:149
session-daemon-go/internal/grpcapi/operation_test.go:353

Behavior:
GetOperation initially copies the stored bytes into legacy_response_json, but typedLegacyCommandReceipt returns an error when the top-level persisted JSON is malformed. GetOperation converts that into gRPC Unavailable and returns no OperationReceipt. Therefore, a durable row known to be completed becomes unreconcilable and its exact native bytes are unavailable to the caller.

Concrete reproduction:
The existing test inserts `{"unterminated"` as the response for a completed operation and asserts that GetOperation returns Unavailable with a nil response at operation_test.go:353-356.

Minimal fix:
Treat typed projection as optional. If a completed row’s envelope cannot be decoded or authenticated, return:

- state = COMPLETED
- exact legacy_response_json bytes
- typed_response unset
- status/error left explicitly null as appropriate

Only storage/read failures should return Unavailable. Add coverage proving malformed bytes are returned unchanged without a synthetic typed receipt.

Confirmed behavior

- Legacy scope is exactly `[action, agent_type, connection_id, session_id]`.
- Absent and explicit null map to legacy JSON null; an explicit empty string remains distinct.
- Missing, uncertain, and completed ledger rows map correctly.
- Uncertain reads do not redispatch and carry a non-retryable failed-precondition business error.
- Completed lookup is read-only from gRPC through protocol/events/journal.
- Durable reservations and completed responses survive reopen.
- The authenticated TCP reopen test verifies zero calls to the replacement runtime.
- Valid unknown native JSON and large integers remain exact bytes.
- Syntactically valid envelopes with wrong action, daemon, session, empty identity, null typed fields, or invalid typed-field types do not produce synthetic typed receipts.
- Unknown protobuf fields are rejected at the request, key, and all nullable scope-wrapper levels.
- The current default server registers exactly DaemonControl, EventStream, and OperationQuery.
- GetCapabilities advertises those same three services and operation_lookup=true.
- Unary and streaming RPCs share bearer authentication.
- No worker/result service is registered by the default server.
- gRPC configuration requires target and credential together and an explicit nonzero loopback address.
- No OperationQuery caller was found in app-server-go; integration remains future work and this review does not establish P2 or full-migration completion.

Suggestions, not defects

- Add explicit table-driven tests for syntactically valid wrong action, wrong daemon ID, wrong session ID, and empty session ID, asserting exact legacy bytes plus no typed response. The implementation currently fails closed, but direct regression coverage is limited.
- Add an app-server-go OperationQuery consumer/integration test before claiming the migration slice is consumed end to end.

Verification

All commands exited 0:

- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/grpcapi -count=1 -timeout=120s -run 'Test(GetOperation|OperationNull|DefaultServerRegistersAndAdvertisesOperationQuery|AuthenticationCapabilitiesAndRegisteredSurface)' -v`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/protocol -count=1 -timeout=120s -run 'Test(ReadStoredOperation|OperationCapabilityAndReadOnlyLookup|FailedNativeOperationRetainsUncertaintyOnRetry|DurableOperationRetryDoesNotCreateTwice)' -v`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/journal -count=1 -timeout=120s -run 'TestOperationReservationSurvivesRestartAndConcurrentRetries' -v`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./cmd/astrorder-sessiond -count=1 -timeout=120s -run 'TestDaemon(ConfiguredGRPCShutdownExitsProcess|RejectsIncompleteOrNonLoopbackGRPCConfig)' -v`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go vet ./internal/grpcapi ./internal/protocol ./internal/journal ./cmd/astrorder-sessiond`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./... -count=1 -timeout=120s`
- `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go vet ./...`
- Contracts: `go test ./... -count=1 -timeout=120s` — 19 tests in 2 packages
- Contracts: `go vet ./...`

Provenance

Branch: master
HEAD: ff32f947024b1840b939ebfb8e58e45886d34b47
Baseline: HEAD plus dirty working tree. Much of the reviewed gRPC/contracts slice is untracked, so these SHA-256 values identify the reviewed artifacts:

- AGENTS.md: d5a76147bc9b924e0f367209db8045076a242a43db0d5876484a9fa57d02ae03
- docs/design/astrorder-full-go-migration.md: ff42fd0bcbd8807d16f2bbaa57468ba9af440421369d6055cc4ac2cdccf1b0ca
- session-daemon-go/internal/grpcapi/operation.go: b6a89ecff9b86ac7527d91de294db974f5ffb420e631bd56a20c8da3327d3794
- session-daemon-go/internal/grpcapi/operation_test.go: ff0f8cf4a2e1b947d0f96997247c3d3ab4c7a174f758a83d678ae4e45753362a
- session-daemon-go/internal/grpcapi/operation_presence_test.go: 6a9f5d9480d0c89e44fa03409b003c878047557bad848d7db086649adbb26999
- session-daemon-go/internal/grpcapi/operation_unknown_test.go: 70542a7c80356da73b2d5dfaa0a5f941bcb49ee275b74b8a4e3f8a32ef85063b
- session-daemon-go/internal/grpcapi/registration_test.go: 1c734bd774e97034db28808f65f02cd46d2a09656690e08d4eb40d024fbf9a8f
- session-daemon-go/internal/grpcapi/server.go: 9e955ed616faa8bf296d47b12c6a26b91d5a38924f2de0a67937866f4b95c03a
- session-daemon-go/internal/grpcapi/control.go: 8528ecafbcb07e40308fbaec5a9bf92cb9439f475e0433a56ad412484a88aa64
- session-daemon-go/internal/grpcapi/grpcapi_test.go: 0b0ac1baa66b7d1b2779d1a1524c7cd3c9607091c0db8a56a12a407082b67993
- session-daemon-go/internal/protocol/operation_facade.go: 7e6b8953bd9d2f0566977c46208c0548eb8a4cd581d7f4317f504fbc69fa0eda
- session-daemon-go/internal/protocol/operation_facade_test.go: cd1d70c872069182a7c8eaaf2a357ce8f473f56939afc15bf20aabeb3afc20c9
- session-daemon-go/internal/protocol/operations.go: e719d101d995ec72f63ecb51f94407ec88d0b757f393ab96c92a4a73093aa957
- session-daemon-go/internal/events/operations.go: 0871212d86683c5d810a11e8244b1db323d4678e6ccf7cf304c02cc8efdbe6c2
- session-daemon-go/internal/journal/operations.go: 8c243051668342ed366d4c50aae6c83ef171b97b25bc114e6c3ee3d36e05a534
- session-daemon-go/internal/journal/operations_test.go: 3379e70ccf623f6c347b8154d2454679990ddd1bf4aad21eca2fe8f829166ce6
- session-daemon-go/cmd/astrorder-sessiond/main.go: be1a2bb98dde87eb8ce991b8521b30e201781253afdda5d4c70d0a6e09ed32b1
- session-daemon-go/cmd/astrorder-sessiond/main_test.go: b08b1417d0aabd00243e9c7f8705f741bffca4c578138b80593799dd2e4a5ccc
- contracts/proto/astrorder/sessiond/v1/sessiond.proto: c484afa2eebd3343d1aea602a66295154c3501bef5efdfeef340d546a1064fb0
- contracts/descriptor_test.go: 6d4cee03f286506b40ebf86349ba7b874e6f5f442804bea87989bc81aa70241b
- contracts/action-map.json: 3753f6f0ae52bc1c94c68992ac8b7eeda2359e436abc593ada069c34009247d1

The source changed concurrently during the review; notably, protobuf unknown-field rejection and its tests appeared before final verification. The hashes above refer to the final tested version.

No repository files, production services, credentials, real inference, SSH targets, or databases were modified or contacted. The two requested scratch reports were not written because the tool-call ceiling was reached immediately before the authorized report writes.