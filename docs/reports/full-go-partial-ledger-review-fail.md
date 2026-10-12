Full-Go partial-ledger final review

Verdict: FAIL

This is a scoped failure of the LOW-finding fix's supplemental-ledger integrity gate. It does not claim P0, P2, F03, production verification, or the full Go migration is complete. Current F03 remains IMPLEMENTED with zero final Evidence and three blockers. Live final verification correctly fails.

Findings

1. MEDIUM — unknown JSON keys fail open and can erase the partial ledger or execution binding

Path: app-server-go/internal/parity/baseline.go:130

The feature matrix is decoded with json.Unmarshal, without DisallowUnknownFields. Two private-copy mutations were accepted with structural ok=true and zero issues:

- partial_implementations renamed to partial_implementation, which removed the entire typed partial group;
- execution renamed to executon, which removed the historical execution object.

This does not create final Evidence or runtime registration, but it defeats the purpose of enforcing claim-bearing partial metadata. Strictly decode the feature matrix and nested partial structures, or explicitly reject unknown keys. Add regressions for both misspellings.

2. MEDIUM — historical command and assertion claims are not bound to the hashed result

Path: app-server-go/internal/parity/partial.go:125

The ledger commands and exit_codes are checked only for non-empty equal cardinality and zero exits. From the hash-bound result, validation reads only passed, daemon_sha256, full_migration_complete and production_changed. It ignores the result's schema_version, stage, commands, assertions, fixture receipt and cleanup.

Consequences reproduced on private copies:

- arbitrary ledger claims such as “production deployment completed” and “full migration complete” passed while retaining the same historical result hash;
- a correctly hashed result with wrong JSON types for schema_version, stage, commands and assertions passed;
- the current ledger has 3 commands, 3 exits and 4 assertions, while the archived result has 1 differently worded command, no exit_codes field and 8 assertions.

Define and strictly decode a complete bounded result schema. Bind every command/exit/assertion claim to the hash-bound artifact, or remove unbound claims from the ledger. Verification must continue never executing ledger commands.

3. LOW — feature-group identity and partial uniqueness are checked only locally

Paths: app-server-go/internal/parity/baseline.go:145; app-server-go/internal/parity/partial.go:58

Private-copy matrices passed with zero issues when:

- the original feature group id was missing;
- F03 was split into two duplicate feature groups with partitioned parent entrypoints, and the same partial ID was reused across groups;
- two partial records claimed the same parent entrypoint.

Require a non-empty unique feature-group ID, define partial-ID scope explicitly, and reject overlapping partial entry claims in that scope.

4. LOW — artifact paths need not be canonical/clean

Path: app-server-go/internal/parity/manifest.go:293

The existing checks correctly reject absolute paths, colon-bearing paths, root escapes, outside-root symlink targets and non-regular files. They still accept internal aliases such as docs/reports/../reports/file.json for review, evidence and process-result bindings. Hash integrity remains intact, so this is not a demonstrated root escape; it is an identity/canonicalization weakness. Require the supplied slash-relative path to equal its clean canonical representation before the existing symlink and regular-file checks.

Verified behavior

- Partial records are preserved at the original feature boundary before per-entry expansion.
- Verify validates partial groups in structural mode.
- The current F03 partial entrypoints are all inside the 44-entry parent set.
- Group-local missing/duplicate ID, invalid stage/status, empty/duplicate entries, missing owner, invalid acceptance and missing explicit legacy disposition regressions pass.
- Review path/hash/PASS=true checks work. A prior-hash review cannot certify REVIEWED.
- DONE and PRODUCTION_VERIFIED are rejected for bounded partials. PRODUCTION_VERIFIED should remain forbidden because this schema records historical isolation with source_freshness_limited=true, synthetic/private fixtures, no production change and no current-source proof.
- Partial records do not enter final Evidence, do not satisfy runtime registrations and do not change parent status.
- The repository process result is byte-identical to the archived scratch result. Both SHA-256 values are 14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e.
- The process result says passed=true, production_changed=false and full_migration_complete=false. Its daemon hash matches the ledger candidate hash. The binary bytes were not available and were not rehashed.

Execution results

- go test ./internal/parity ./cmd/astrorderctl -count=1 -timeout=60s: exit 0; both packages passed.
- go vet ./internal/parity ./cmd/astrorderctl: exit 0; no diagnostics.
- Focused labelled partial tests: exit 0; all tests and subtests passed. Their fixtures explicitly state that they are not real executions or reviews.
- Live structural verification: exit 0, ok=true, 472 items, 472 entries, zero issues.
- Live final verification: exit 1, ok=false, 3632 issues, including 458 unregistered entrypoints and 472 unfinished items.
- One initial reviewer pipeline failed because jq was absent. No install was performed; verification was rerun successfully using raw JSON and Node parsing. This was not a product failure.

Stable scoped SHA-256

- app-server-go/internal/parity/baseline.go: e3c4ce6a140f7f748d0ca751559c76cbb6728ba21b8882d6174d736bc44a8663
- app-server-go/internal/parity/manifest.go: a84be03f51fac4e5ee5f7b40e201c50f7d3f632ef339be5173f86d437d887920
- app-server-go/internal/parity/partial.go: 66ed09fec6157c26a32ce8dc1437930e72a70462b7c56b9ec885923c16452944
- app-server-go/internal/parity/partial_test.go: 1e1e460218c12d6579e8f388bb27f7532945f7e4eaad556ed23df9716fe9b36c
- docs/reports/full-go-daemon-evidence-review.json: a629edddc2d2713759f49f815d3c1aaa4f4a7f9b18f255a43943f61990bab5f9
- docs/reports/full-go-migration-matrix.json: 02dfe6f1f485a1f28d24aaa1ea81c0139c9c4000f3bc637d78f051be4b2a000c
- docs/reports/full-go-p2-operation-process-result.json: 14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e

Hashes matched at initial capture and final pin.

Limitations

This was read-only. No repository source/report edit, commit, push, stash, service, production action, production database/chat/secret access, real inference, install or other profile change occurred. Private scratch mutations are schema fixtures only. The earlier RED state was not reconstructed, so only current GREEN behavior was observed. Historical ledger commands were never executed. Candidate binary bytes were not available; only declared/result hash agreement was checked.
