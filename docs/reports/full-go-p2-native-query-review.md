# Independent Read-Only Review: RuntimeQuery.QueryNative

Scope: 11 files (`session-daemon-go/internal/grpcapi/{native_query.go, native_query_translate.go, native_query_test.go, native_query_translate_test.go, native_query_boundary_test.go, native_query_routes_test.go, server.go, control.go, grpcapi_test.go}`, `session-daemon-go/internal/protocol/{query_facade.go, control.go}`).

---

### Verdict: PASS

No high- or medium-severity logic defects were identified in the scoped `RuntimeQuery.QueryNative` implementation. The implementation satisfies all safety, admission, translation, and validation constraints.

---

### Scope & Invariant Verification Summary

1. **Closed Action Map & Family Validation**:
   - Static mapping table (`nativeQuerySpecs`) explicitly defines 17 query branches across `codex` (9), `hermes` (7), and `grok` (1).
   - Rejecting arbitrary native method dispatch; cross-family mismatch rejected before touching the runtime adapter.
   - 3 remote SSH kinds (`RUNTIME_KIND_CODEX_SSH`, `RUNTIME_KIND_SSH_HERMES`, `RUNTIME_KIND_GROK_SSH`) route cleanly through multiplex registry to underlying family adapters.

2. **JSON Options & Parameter Smuggling Prevention**:
   - `rejectNativeQueryUnknown` verifies no unknown protobuf fields are present.
   - Native JSON `options` only allows dynamic native options; typed fields cannot be overwritten or smuggled via JSON options.
   - Single JSON object validated with `io.EOF` and decoded with `decoder.UseNumber()`, preserving 64-bit integer precision.
   - `NullableString` with `NULL_VALUE_NULL` explicitly maps to `nil` in params, distinguishing explicit null from omitted fields.

3. **Size & Business Limits**:
   - Request wire size bounded by `MaxWireBytes` (33554432).
   - Options payload, top-level request payloads, and serialized query results bounded by `MaxBusinessBytes` (16000000).

4. **SSH Transport Strictness**:
   - Unsupported SSH fields (`options`, `ssh_config_alias`, `ssh_executable`) return gRPC `Unimplemented` rather than being silently ignored.

5. **Shared Admission, Drain & Cancellation**:
   - Query dispatch gates on `beginControl()` / `endControl()`.
   - Rejects queries with `FailedPrecondition` when daemon drain/shutdown is pending.
   - Cancellation cleanly unwinds context and releases admission locks without leaking leases. Test `TestQueryNativeCancellationReleasesNativeAdmission` polls authoritative shutdown admission up to 3s to guarantee complete release.

6. **Target Preservation & Idempotency / Read-Only**:
   - Response target is an exact clone of the validated request target (`proto.Clone`).
   - Query native execution uses shared read-only route; no session creation, operation rows, or WAL tracking mutations occur.

---

### Limitations & Boundary Notes
- **Synthetic TCP Routing Only**: Tests are synthetic routing proofs; no live model inference or remote daemons were executed.
- **Unimplemented Methods**: Only `RuntimeQuery.QueryNative` is implemented; remaining `RuntimeQuery` methods return `Unimplemented`.
- **Partial Scope**: Scoped solely to `RuntimeQuery.QueryNative`; this does not certify full P2 or full Go migration completion.
- **Production State**: Zero repository edits (`production_changed: false`).

---

### Source Hashes (SHA-256)
- `session-daemon-go/internal/grpcapi/native_query.go`: `027b5d27c0406850a99e9f4ee5f2060e855f29cc060dd684b7108433b9cc273b`
- `session-daemon-go/internal/grpcapi/native_query_translate.go`: `175331278a5cd206845fddf0f06fbe28a793d2294f4ddb42ec75a53163a65997`
- `session-daemon-go/internal/grpcapi/native_query_test.go`: `15f6b0206446c73bb9f94728514905c081230aef06bcf44df48d046cc9ac8da7`
- `session-daemon-go/internal/grpcapi/native_query_translate_test.go`: `7ee80a5df7f901528bc2fe925fa2a2a32758fcae83204e2133516f8d65be26dd`
- `session-daemon-go/internal/grpcapi/native_query_boundary_test.go`: `20a6fb3b241efeac514df4e733a27d4374631a58d5552f9d8b609070c509f93c`
- `session-daemon-go/internal/grpcapi/native_query_routes_test.go`: `326cd62e547282399111b1fd2015a5194006a7408af62ca1997f7273263f4ccb`
- `session-daemon-go/internal/grpcapi/server.go`: `74fc9fe4b5b4f2cf735d398a9de4843cbd8929bca6709d73a3370c2a9ff9ddc6`
- `session-daemon-go/internal/grpcapi/control.go`: `7d46dd37d1a0fd602d885b95982fe7c60467a05b169e5d619cf3faae2915c825`
- `session-daemon-go/internal/grpcapi/grpcapi_test.go`: `bf3770cf1800a32381f690919b498a6cb7b0d7c3b003047f1862f1a13e877f7b`
- `session-daemon-go/internal/protocol/query_facade.go`: `6f9f3e34aad2e6916c659d648ac2fb93560ed2f70bc904ad40a76da9c831f037`
- `session-daemon-go/internal/protocol/control.go`: `3983d1e52ecf435253077685976060a05dd64f0dc6f7efa0c97f93d4d187b5aa`

---

### Command Verification
1. `go test ./internal/grpcapi ./internal/protocol -run 'TestQueryNative|TestNativeQueryTranslation|TestRuntimeQueryUsesReadOnlyNativeRoute|TestAuthenticationCapabilitiesAndRegisteredSurface|TestDefaultServerRegisters' -count=1 -timeout=60s` -> Exit Code `0`
2. `go vet ./internal/grpcapi ./internal/protocol` -> Exit Code `0`
