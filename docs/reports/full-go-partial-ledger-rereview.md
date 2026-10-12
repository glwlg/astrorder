# Independent Read-Only Rereview: PARTIAL-001 Through PARTIAL-004

Verdict: **PASS** (strictly scoped to PARTIAL-001 through PARTIAL-004; does not certify P0, P2, F03, or full Go migration completion).

Parent feature `migration:f03` remains `IMPLEMENTED` with 0 final Evidence entries and 3 active blockers. Live final verification continues to fail closed (3,632 issues, exit code 1), while structural verification passes (472 items / 472 entries, exit code 0).

---

## 1. Scope and Executive Summary

This independent read-only rereview evaluates the fixes applied to resolve defects PARTIAL-001 through PARTIAL-004 recorded in `docs/reports/full-go-partial-ledger-review-fail.json` and `.md`.

* **PARTIAL-001 (Resolved)**: Strict JSON object decoding now rejects unknown claim-bearing fields (including typos such as `partial_implementation` and `executon`) and trailing garbage across manifest and partial schemas, while preserving opaque contract `json.RawMessage`.
* **PARTIAL-002 (Resolved)**: Unbound commands, exit codes, and assertions have been removed from `PartialExecution` and the migration matrix. The historical process result is bound by exact hash and strictly decoded against a complete schema (v1, `P2-partial`, non-empty commands/assertions/receipt/cleanup, mandatory `false` completion/production). The original archived result remains byte-exact SHA-256 `14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e`.
* **PARTIAL-003 (Resolved)**: Manifest decoding enforces non-empty, globally unique parent item IDs and feature IDs before expansion. Group-level partial validation prevents duplicate partial IDs and rejects overlapping entrypoint claims across partial slices.
* **PARTIAL-004 (Resolved)**: Artifact path resolution rejects non-canonical slash-relative aliases (`..`, `.`, backslashes, double slashes) prior to symlink evaluation and root containment checks.

---

## 2. Actual Reviewer Execution & Verification

### Focused Parity & Ctl Tests + Vet
Executed from `P:/workspace/glwlg/ai/astrorder/app-server-go` using `C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe`:

* `go test ./internal/parity ./cmd/astrorderctl -count=1 -timeout=60s`:
  * Exit code: `0`
  * Output:
    ```
    ok  	astrorder.dev/app-server/internal/parity	0.647s
    ok  	astrorder.dev/app-server/cmd/astrorderctl	0.503s
    ```
* `go vet ./internal/parity ./cmd/astrorderctl`:
  * Exit code: `0`
  * Output: clean (no diagnostics)

### Coordinator Live CLI Fixture Inspection
The coordinator suite recorded in `C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-partial-ledger-rereview-after/result.json` was inspected:
* 27 total private mutations tested against `astrorderctl`.
* 27 mutations rejected with exit code 1 (`misspelled-partials`, `misspelled-execution`, `unknown-top-field`, `unknown-review-field`, `unknown-partial-field`, `wrong-typed-partials`, `missing-parent-id`, `duplicate-group-partitioned-entries`, `duplicate-partial-id`, `overlapping-entry`, `alias-review-path`, `alias-evidence-path`, `alias-process-path`, `unknown-process-key`, `wrong-process-schema-type`, `unsupported-process-schema`, `wrong-process-stage-type`, `wrong-process-commands-type`, `wrong-process-assertions-type`, `empty-process-commands`, `empty-process-assertions`, `missing-process-cleanup`, `production-process-claim`, `full-process-completion-claim`, `redundant-unbound-commands`, `partial-production-status`, `partial-done-status`).
* Unchanged private matrix passed (exit code 0).
* *Note*: These 27 cases were executed by the coordinator process and inspected during rereview; they were not re-executed directly in this session.

---

## 3. Scoped Source Hashes

Confirmed via SHA-256 calculation:

| File Path | SHA-256 Hash |
| :--- | :--- |
| `app-server-go/internal/parity/baseline.go` | `08aa1d4274a543c8b559460973ed06dc8c14c790d82427a4b94e6342752d16d7` |
| `app-server-go/internal/parity/manifest.go` | `dcb19f45d2ad55f26d5ccb8f1d31cf6aef3b1992aaeeb62d88e68b3f0d956d1d` |
| `app-server-go/internal/parity/partial.go` | `39ef10d73f3eb30eaf412ffc4e493f93093e6053bb40844750434571476b9a69` |
| `app-server-go/internal/parity/partial_test.go` | `62452520e98d6207b7cc8d05f6ed3ee51feaacefcdd4c6600328f36269e4796f` |
| `app-server-go/internal/parity/jsondecode.go` | `9358e3c1ef99daefaa546949a6a59583fec30938f69939b16010e746b2a80cca` |
| `app-server-go/internal/parity/strict_test.go` | `93dc8280bb485071f1b7e123d02ad6f73d8595dfef370f903e00d5e6a42142c1` |
| `app-server-go/internal/parity/partial_identity_test.go` | `11ec9b7aac1d73afcafbe75abe94ddf9af0b91d0cbf3b5895c1dcb8604dfff3e` |
| `app-server-go/internal/parity/process_result_test.go` | `17b78438ce553713fbcc82342cb15d147817017489e1b84af491ec8da2089731` |
| `docs/reports/full-go-migration-matrix.json` | `c1c78d416f74818d4598c18e3e28a295aaf2fbd98fb9888a75dfe83dee67ddb2` |
| `docs/reports/full-go-p2-operation-process-result.json` | `14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e` |

The historical process-result artifact matches the exact pinned hash.

---

## 4. Status Gate Assessment

* **Parent `migration:f03`**: Status `IMPLEMENTED`, 44 entrypoints, 0 final `evidence` entries, 3 blockers retained.
* **Structural Verification**: `ok: true`, `final: false`, 472 items / 472 entries, 0 issues.
* **Final Verification**: `ok: false`, `final: true`, 3,632 issues (including 458 unregistered entrypoints, 472 unfinished items, 44 blocked items). Correctly fails closed.
* **Execution Boundary**: No commands from the partial execution ledger were executed. Historical results remain documentary.

---

## 5. Limitations

1. This was a read-only review; no source files, matrix records, or production configs outside scratch were edited.
2. PASS verdict is strictly limited to PARTIAL-001 through PARTIAL-004.
3. Does not certify completion or acceptance of P0, P2, F03, or the full Go backend migration.
4. Historical hash matching does not cryptographically certify author runtime execution; trusted-runner separation remains authoritative.
