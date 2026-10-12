# Astrorder session-daemon v1 typed contracts

Status: implemented P1 contract surface; not declared frozen and not a claim that the P9 cutover is complete.

This module defines the typed protobuf/gRPC boundary for migrating the App/Session Daemon transport without duplicating the Go kernel. It was reconciled at source revision `ff32f947024b1840b939ebfb8e58e45886d34b47` against the Go dispatchers, runtime adapters, current App callers, Grok connector, tests, the P0 inventory, and `docs/design/astrorder-full-go-migration.md`.

## Artifacts

- `proto/astrorder/sessiond/v1/sessiond.proto`: v1 services and owned messages.
- `action-map.json`: request, response, event, alias, native-query, and App-direct disposition namespaces.
- `gen/go`: generated Go messages, clients, and server interfaces.
- `gen/python`: transitional generated Python messages and stubs. This output is development-only and is removed after the Python App migration.
- `descriptor_test.go`: service, action, alias, oneof, observer, and forbidden-surface checks.
- `roundtrip_test.go`: presence, explicit-null, exact integer, native bytes/hash, stream, connector, terminal, SSH, and fixed-limit checks.
- `generation_test.go`: two independent generation runs and SHA-256 comparison against checked-in output.

## Contract boundaries

There is no generic `Call(action, json)` RPC. Native runtime discovery is a closed `NativeQueryRequest.query` oneof. State-changing Codex `desktop/submit` and `desktop/settings` are separate mutation RPCs with `CommandMeta.operation_id`.

Native-owned extensions use `NativeJson { schema, bytes utf8_json, optional hash, optional sha256 }`. Go translators must validate the bytes as exactly one JSON value using `json.Decoder.UseNumber`; they must not unmarshal native identifiers through `float64`. `ExactIdentifier` provides exact string, signed, unsigned, arbitrary decimal, or opaque native JSON identity.

Proto3 `optional` represents absent versus present-empty/zero/false. `NullableString`, `NullableSessionStatus`, and `NullableBusinessError` represent absent versus explicit null versus a concrete value. Operation lookup scope, status/error, cursors, and model-config digests retain the required distinctions.

SSH configuration is typed. `identity_file` and executable/path aliases are references only; no key contents, passphrases, tokens, or credentials are contract fields. Implementations must redact connection settings from logs.

The maximum compatibility business payload is 16,000,000 bytes. The gRPC send/receive limit must be configured to at least 33,554,432 bytes on both clients and servers. That runtime setting is not automatically changed by generated code.

## Resolved migration dispositions

- Observer reporting uses `ObservedActiveStatus`, which can express only positive `RUNNING` or `WAITING_APPROVAL` evidence for an owned session. Absence from a native active list is inconclusive and cannot report idle.
- `session.approval.respond` and native Hermes `approval.respond` both map to `SessionControl.RespondApproval`; neither creates a duplicate RPC.
- Per-session App shutdown maps to `DetachSession`, not runtime teardown. Connection/runtime teardown uses guarded `DisconnectRuntime` or `DisconnectConnector`.
- Grok history and deletion remain Go App-native file reader/deleter capabilities.
- WSL and SSH model-config targets stay typed, but plan/apply must return `UNSUPPORTED_UNTIL_GO_EXECUTOR` until the embedded Python remote executor is replaced.
- Existing ledger rows keep their current scope, JSON hash, and stored response schema. `LEGACY_JSON_V1` names that reader path. New typed mutations use `ASTRORDER_OPERATION_V1`; protobuf wire bytes are never the canonical hash input.
- Event streaming models snapshot, replay, live-ready, live, resync-required, and overflow explicitly. Session/event sequence values are `int64`.
- Terminal transport is one bidirectional stream with open, bytes input/output, resize, interrupt, explicit close, exit, overflow, and reconnect cursor frames.
- Connector ingress is typed bidirectional gRPC hello/heartbeat/event/disconnect with acknowledgements and explicit control-session binding. The old daemon connector WebSocket is not a fallback for the migrated route.

## Pinned toolchain

- Buf `v1.66.1`
- `protoc-gen-go v1.36.12`
- `protoc-gen-go-grpc v1.6.2`
- `google.golang.org/grpc v1.84.0`
- `google.golang.org/protobuf v1.36.12`
- transitional `grpcio-tools==1.84.0` and `grpcio==1.84.0`

`buf.gen.yaml` uses the portable plugin names `protoc-gen-go` and `protoc-gen-go-grpc`. The pinned versions must be on `PATH`, or the root build scripts can receive their executable paths through `ASTRORDER_PROTOC_GEN_GO` and `ASTRORDER_PROTOC_GEN_GO_GRPC`; those scripts write a temporary runtime Buf template and leave the checked-in template portable.

## Generate and verify

From `contracts/`, with the pinned tools on `PATH`:

```text
buf lint
buf build
ASTRORDER_VERIFY_GENERATION=1 ASTRORDER_GRPC_TOOLS_PYTHON=python go test ./... -count=1
```

For transitional cross-language verification, set `ASTRORDER_INTEROP_PYTHON` to the same `grpcio-tools==1.84.0` interpreter. The test serializes in Go, decodes and modifies in a Python subprocess using the generated module, and decodes the returned bytes in Go.

The root build scripts default to development mode: Go and transitional Python generation, deterministic generation, and Go/Python interoperability are required. Tool locations are selectable with `ASTRORDER_BUF`, `ASTRORDER_GO`, `ASTRORDER_PROTOC_GEN_GO`, `ASTRORDER_PROTOC_GEN_GO_GRPC`, and `ASTRORDER_GRPC_TOOLS_PYTHON`.

`ASTRORDER_GO_ONLY=1` is the P9-compatible path: it verifies only checked-in Go bindings and does not resolve, import, or run Python. `ASTRORDER_GO_OUTPUT_DIR` selects the retained output parent; the default is the ignored `.runtime/go-build`. A successful build publishes a new candidate directory containing `astrorder-sessiond.exe`, `astrorder-app.exe`, `astrorderctl.exe`, `astrorder-agent.exe`, `astrorder-helper.exe`, and `SHA256SUMS`. A failed test, vet, or build removes only its unique partial directory and never writes a success manifest.

## Intentional Buf lint exceptions

The design fixes concise service names (`DaemonControl`, `SessionControl`, and peers) and deliberately reuses typed receipts/stream frames. `buf.yaml` therefore excepts only `SERVICE_SUFFIX`, `RPC_REQUEST_STANDARD_NAME`, `RPC_RESPONSE_STANDARD_NAME`, and `RPC_REQUEST_RESPONSE_UNIQUE`. Standard package, enum, field, and type lint rules remain enabled.
