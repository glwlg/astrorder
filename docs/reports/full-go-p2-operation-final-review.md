# Bounded P2 OperationQuery final rereview

Result: PASS for the scoped OperationQuery fix.

`passed: true`

No high- or medium-severity defect remains in the reviewed final OperationQuery read path. This is a fix-verification result only: it does not mark P2 or the full Go migration complete, and it does not establish App/services integration.

## Resolved finding

The previous medium finding is resolved.

A completed ledger row is now separated from its optional typed projection:

- `session-daemon-go/internal/grpcapi/operation.go:34-36` maps an actual `ReadStoredOperation` failure to gRPC `Unavailable`.
- `session-daemon-go/internal/grpcapi/operation.go:38-43` initializes `error` and `status` as explicit protobuf null wrappers.
- `session-daemon-go/internal/grpcapi/operation.go:54-63` marks the known row `COMPLETED`, copies `legacy_response_json` exactly, and returns that receipt if typed projection cannot decode the envelope.
- `session-daemon-go/internal/grpcapi/operation_test.go:336-362` proves valid untyped and malformed completed bytes survive an authenticated TCP lookup with no synthetic typed receipt.
- `session-daemon-go/internal/grpcapi/operation_test.go:399-405` separately proves a closed durable store still returns gRPC `Unavailable` and no receipt.

A private scratch-only TCP regression also exercised these completed payloads:

- a valid JSON object followed by trailing data;
- a JSON array;
- a JSON string;
- JSON null.

All four remained `COMPLETED`, preserved exact bytes, left `typed_response` unset, and returned explicit null `error` and `status`. This directly covers malformed, trailing-data, and non-object receipts rather than inferring behavior only from the decoder.

## Confirmed retained behavior

- Unknown protobuf identity fields are rejected at the request, key, and all nullable scope-wrapper levels.
- Absent and explicit-null scope fields preserve legacy JSON null; explicit empty string remains a distinct scope value.
- Missing, uncertain, and completed rows remain distinct.
- Uncertain reads do not redispatch and return a non-retryable failed-precondition business error.
- Completed lookup is read-only through gRPC, protocol, events, and journal; reopen lookup does not execute replacement runtime work.
- Valid unknown native JSON and large integers retain exact bytes.
- Null or invalid typed fields do not manufacture typed values.
- Default registration and capability advertisement agree on exactly `DaemonControl`, `EventStream`, and `OperationQuery`, with `operation_lookup=true`.
- OperationQuery is bearer-authenticated on the actual TCP gRPC server.

## Findings

None.

## Verification

Every command below exited 0.

1. Scoped authenticated TCP OperationQuery regressions:

   `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/grpcapi -count=1 -timeout=120s -run 'Test(GetOperationPreservesUntypedAndMalformedCompletedBytes|GetOperationReadsLegacyWSLedgerOverAuthenticatedTCPAndAfterReopen|GetOperationPreservesUncertainAndExactScopeWithoutRedispatch|GetOperationRejectsInvalidPresenceCanonicalizationAndUnavailableStorage|OperationNullFieldsNeverBecomeSyntheticTypedValues|OperationLookupRejectsUnknownIdentityFields|DefaultServerRegistersAndAdvertisesOperationQuery)$' -v`

   Exit: 0. All selected tests and subtests passed; package result `ok astrorder.dev/session-daemon/internal/grpcapi`.

2. Protocol/journal scope, persistence, uncertainty, and no-redispatch regressions:

   `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/protocol ./internal/journal -count=1 -timeout=120s -run 'Test(ReadStoredOperationUsesExactLegacyScopeAndNeverDispatches|ReadStoredOperationReportsUncertainAndValidatesWithoutMutation|OperationCapabilityAndReadOnlyLookup|FailedNativeOperationRetainsUncertaintyOnRetry|DurableOperationRetryDoesNotCreateTwice|OperationReservationSurvivesRestartAndConcurrentRetries)$' -v`

   Exit: 0. All selected tests passed; both package results were `ok`.

3. Focused static analysis:

   `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go vet ./internal/grpcapi ./internal/protocol ./internal/events ./internal/journal`

   Exit: 0. No diagnostics.

4. Private scratch-only trailing/non-object TCP regression against a copy of the final hashed source:

   `env -u ASTRORDER_SSH_REAL_TEST -u ASTRORDER_TEST_REAL_SSH -u ASTRORDER_REAL_HERMES_PYTHON -u ASTRORDER_REAL_HERMES_SOURCE -u ASTRORDER_LATENCY_OUTPUT go test ./internal/grpcapi -count=1 -timeout=120s -run '^TestPrivateUnprojectableCompletedReceiptsPreserveExactBytes$' -v`

   Working directory: `C:/Users/luwei/AppData/Local/hermes/cache/scratch/p2-operation-final-review.pyGd5Q/session-daemon-go`

   Exit: 0. Subtests `trailing`, `array`, `string`, and `json-null` all passed.

No repeated full-module/repository gate was run. This rereview was deliberately bounded, and prior broad green runs were not used as proof for this final source.

## Remaining work and limits

- No `OperationQuery`, `GetOperation`, or `operation_lookup` consumer was found in `app-server-go`. App/service integration remains to be implemented and verified before calling this capability end-to-end consumed.
- Remaining P2 services and the complete T01-T08 acceptance set are outside this review. P2 is not marked complete.
- The wider full-Go migration, real-agent validation, packaging, deployment, and production cutover are not established here.
- No production/service process, real inference, SSH target, chat, secret, or non-fixture database was accessed or changed.

## Provenance and final SHA-256 pin

Branch: `master`

HEAD: `ff32f947024b1840b939ebfb8e58e45886d34b47`

The worktree is dirty and much of this slice is untracked, so the hashes below—not HEAD alone—identify the reviewed source. Scoped hashes matched before testing and at final pin.

- `AGENTS.md`: `d5a76147bc9b924e0f367209db8045076a242a43db0d5876484a9fa57d02ae03`
- `docs/design/astrorder-full-go-migration.md`: `ff42fd0bcbd8807d16f2bbaa57468ba9af440421369d6055cc4ac2cdccf1b0ca`
- `contracts/proto/astrorder/sessiond/v1/sessiond.proto`: `c484afa2eebd3343d1aea602a66295154c3501bef5efdfeef340d546a1064fb0`
- `session-daemon-go/internal/grpcapi/operation.go`: `b736dde6e322fb68cb960c1e09bab59f90afcbfae4166e5fbfa27a0ca8237661`
- `session-daemon-go/internal/grpcapi/operation_test.go`: `94d8cd66a04b5c1ec476126f8759312939efcd29c0084c70621a1d7bb1cfb96a`
- `session-daemon-go/internal/grpcapi/operation_presence_test.go`: `6a9f5d9480d0c89e44fa03409b003c878047557bad848d7db086649adbb26999`
- `session-daemon-go/internal/grpcapi/operation_unknown_test.go`: `70542a7c80356da73b2d5dfaa0a5f941bcb49ee275b74b8a4e3f8a32ef85063b`
- `session-daemon-go/internal/grpcapi/server.go`: `9e955ed616faa8bf296d47b12c6a26b91d5a38924f2de0a67937866f4b95c03a`
- `session-daemon-go/internal/grpcapi/control.go`: `8528ecafbcb07e40308fbaec5a9bf92cb9439f475e0433a56ad412484a88aa64`
- `session-daemon-go/internal/grpcapi/registration_test.go`: `1c734bd774e97034db28808f65f02cd46d2a09656690e08d4eb40d024fbf9a8f`
- `session-daemon-go/internal/protocol/operation_facade.go`: `7e6b8953bd9d2f0566977c46208c0548eb8a4cd581d7f4317f504fbc69fa0eda`
- `session-daemon-go/internal/protocol/operation_facade_test.go`: `cd1d70c872069182a7c8eaaf2a357ce8f473f56939afc15bf20aabeb3afc20c9`
- `session-daemon-go/internal/protocol/operations.go`: `e719d101d995ec72f63ecb51f94407ec88d0b757f393ab96c92a4a73093aa957`
- `session-daemon-go/internal/protocol/operation_test.go`: `4423004afedcb5fa04d3b87208297be7529bcfde8d3d146f9712984e7dad61ed`
- `session-daemon-go/internal/events/operations.go`: `0871212d86683c5d810a11e8244b1db323d4678e6ccf7cf304c02cc8efdbe6c2`
- `session-daemon-go/internal/journal/operations.go`: `8c243051668342ed366d4c50aae6c83ef171b97b25bc114e6c3ee3d36e05a534`
- `session-daemon-go/internal/journal/operations_test.go`: `3379e70ccf623f6c347b8154d2454679990ddd1bf4aad21eca2fe8f829166ce6`

The original FAIL report was preserved without modification:

`C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p2-operation-review-final-extracted.md`

SHA-256: `d75c9b78f50b2c1c7f7054ba988faf3f892cf783bc5e1f1900cf848347bdb408`

No repository files were edited by this rereview.
