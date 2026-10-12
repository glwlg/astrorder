# Independent Read-Only Review: Go P2 Session Read RPCs and Pagination

- **Verdict**: PASS
- **Schema Version**: 1
- **P2 Complete**: false
- **Full Migration Complete**: false
- **Production Changed**: false
- **Business Max Bytes**: 16000000
- **Wire Max Bytes**: 33554432

---

## 1. Scope & Hashed Baseline

The review inspected 22 implementation and test files without modifying any repository files, databases, or production environments:

| Path | SHA-256 Hash |
| :--- | :--- |
| `session-daemon-go/internal/grpcapi/session_read.go` | `1c3f9640e3f40eb52d091a404e02b90a5e038153becffdf5aee14560edd5c454` |
| `session-daemon-go/internal/grpcapi/session_read_history.go` | `964d1e4441265d53619ccc2e3e719c282a5f5e5f9c73c794d95027dd243d5bf7` |
| `session-daemon-go/internal/grpcapi/native_query.go` | `022c6cc01cd89dcdb1a5da9dc4976b60882ed4d4ccda421dd60838863b7636cb` |
| `session-daemon-go/internal/grpcapi/native_query_test.go` | `a7f1ee2c30e18fd91d5798f4be77874733876163b36cfbe0943dc23b8dacfef2` |
| `session-daemon-go/internal/grpcapi/session_read_test.go` | `5c790d80606b7f53b4077d1142f044c4565e34f0c53d81c51be527a747f0d5fe` |
| `session-daemon-go/internal/grpcapi/session_read_model_test.go` | `e3e7ad7ff2fa6d93f50397a429d2eed30f95edf3a061a1bff2c246ad7ed11599` |
| `session-daemon-go/internal/grpcapi/session_read_commands_test.go` | `4783afd26c0ad693707d8fc050a06322c5528353e61673699ad6b7b6bbfe63e8` |
| `session-daemon-go/internal/grpcapi/session_read_history_test.go` | `54a637abcfaacadb1330c819f303f18f31ba918c33b1a3090b0add7b27dee053` |
| `session-daemon-go/internal/grpcapi/session_read_identity_test.go` | `c4bcddb86df9430abb9d9985cb3a5eebeaa0976cfefdc60e4850c8e5ee84b9ac` |
| `session-daemon-go/internal/grpcapi/session_read_boundaries_test.go` | `1a1d911eaea168ae609c471e333f9aa02b64bfbd65ecf52fbef00383f9d6a311` |
| `session-daemon-go/internal/grpcapi/session_read_native_test.go` | `0f162a9469b0b46912b90a4af91a99900d6aead45b4973f3feefd0c585de0e9c` |
| `session-daemon-go/internal/protocol/session_read_facade.go` | `c13c2dae52443294cd69c97122fdb58e9bae7d72c5f07423096c47412348a6f0` |
| `session-daemon-go/internal/runtime/session_read.go` | `a8df5ca10ad872df04c99a936165f7941e3ed6527798968d7fab853d04a68cea` |
| `session-daemon-go/internal/runtime/session_read_test.go` | `c04175f18e10ed088a40b80facc3f8dae0a3726ec859dd1679e753b1895cd6dc` |
| `session-daemon-go/internal/runtime/registry.go` | `45e7d0d02fccb69024e5853e52bf41b24c17460d26747b530d180635e5254707` |
| `session-daemon-go/internal/runtime/control.go` | `405ebbbd0d3c9eff7212ebe09620e95fe2791a0f642cc5ca202ded05e240c44e` |
| `session-daemon-go/internal/runtime/disconnect_action.go` | `fd458260acc68892c2ac4451baf28673691b79b208bb85ae227d48a2bce390f9` |
| `session-daemon-go/internal/runtime/codex/adapter.go` | `372c3e4c7def832cf9f1af54ca512756e08f792fd5a33c804748ce821c551df2` |
| `session-daemon-go/internal/runtime/codex/history_page.go` | `5e723416f363e363c7d38074531bf9ef486ccda5da639970656fd160f726c27a` |
| `session-daemon-go/internal/runtime/codex/history_page_test.go` | `71d26778a2ce97b9b3c448feae836e0675f12b9f0c076ff0253c095712fb07a0` |
| `session-daemon-go/internal/runtime/hermes/adapter.go` | `157b6d856e5e21efa892892d09db266b6c357b540e7cd497eab8d52f04f5ec09` |
| `session-daemon-go/internal/runtime/hermes/history_gateway.go` | `c160d3286e66d230b282576dde6c4d5fa44a93f38efe6d3a17a7d3b607441c84` |

---

## 2. Execution & Checks Summary

1. **Full Test Suite Across Target Packages**:
   - `go test ./internal/grpcapi ./internal/runtime ./internal/runtime/codex ./internal/runtime/hermes ./internal/protocol -count=1 -timeout=90s`
   - Exit code: 0
   - All tests passed: `grpcapi` (7.286s), `runtime` (0.377s), `codex` (2.649s), `hermes` (7.710s), `protocol` (4.692s).
2. **Focused Test Run**:
   - `go test ./internal/grpcapi ./internal/runtime ./internal/runtime/codex ./internal/runtime/hermes ./internal/protocol -run "TestSessionRead|TestOwnedHistoryPage" -count=1 -timeout=60s`
   - Exit code: 0
3. **Static Analysis**:
   - `go vet ./internal/grpcapi ./internal/runtime ./internal/runtime/codex ./internal/runtime/hermes ./internal/protocol`
   - Exit code: 0 (clean output, 0 warnings/errors).

---

## 3. Analysis & Invariants Verified

- **Session Read Receipts & State Journal**: Read lease uses existing native `Commander.Command` under shared admission, but explicitly avoids changing `r.states`, modifying session status, or appending to durable journal logs.
- **Identity Enforcement Before Dispatch**: Validates `session_id` length and matches caller `AgentId` and `ConnectionId` against registry-bound identities before contacting adapters. Unknown protobuf fields are rejected via recursive reflection prior to dispatch.
- **History Pagination & Cursors**:
  - Codex reverses descending native rows to chronological ordering, limits requests to `[1, 200]`, and signs cursors with base64 URLEncoding containing `{thread, agent, cursor}` prefixed with `codex:`.
  - Hermes SQLite path reads active/compacted history within a query-only transaction (`PRAGMA query_only=ON`).
  - Hermes RPC gateway fallback windowing matches rows against a SHA-256 hash anchor row (`hermes-rpc:` cursor) to maintain stable pagination despite message additions.
- **Null and Type Fidelity**: JSON decoding preserves `json.Number` for 64-bit integer preservation. Explicit null fields in native objects are preserved in `NativeJson`.

---

## 4. Limitations & Non-Claims

1. **Hermes Gateway Transcript Limit**: Gateway fallback fetches the full transcript (`session.history`) into memory before windowing; the upstream ~4MB JSON scanner limit persists for large sessions where local SQLite is unavailable.
2. **Codex Legacy Versions**: Codex native versions lacking `thread/items/list` are unsupported without fallback.
3. **Synthetic Fixtures**: All tests utilize synthetic in-memory or mock processes; no live remote inference or production daemon parity is proved.
4. **Protobuf Optional Nulls**: Protobuf optional string fields cannot differentiate between null and absent; explicit null values are retained in the raw `NativeJson` payload.
5. **RuntimeQuery Surface**: Mutation RPCs (`SetModel`, `SetReasoning`, `SetApprovalMode`, `DisconnectRuntime`) and `ReadApprovalMode` remain `Unimplemented`. No `ExecuteCommand` RPC is defined in `RuntimeQuery`.
6. **Coordinator Scope**: The cross-language coordinator test harness is excluded and not certified by this review.
