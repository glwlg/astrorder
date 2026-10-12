Full Go daemon evidence command review

Verdict: PASS WITH ONE LOW-SEVERITY FINDING

No high- or medium-severity defect was found in the scoped daemon command resolver, its regression test, or the reviewed F03 ledger data. The result approves a code-owned command shape only. It does not prove daemon command execution, F03 completion, P2 completion, production verification, or full Go migration.

Finding

LOW — supplemental partial_implementations claims are not parsed or verifier-enforced.

app-server-go/internal/parity/baseline.go:106-126 defines the feature ledger adapter without partial_implementations. Its conversion at baseline.go:143-168 transfers only parent fields, so JSON decoding ignores the nested statuses, review/execution metadata, source_freshness_limited, and legacy_removed values at full-go-migration-matrix.json:636-702.

Impact: a future typo or stale nested claim would not be rejected by astrorderctl migration verification. The current entries were manually checked and do not produce a false pass: parent F03 remains IMPLEMENTED, evidence is empty, three blockers remain, and final verification separately requires DONE, bound evidence, an artifact, independent review, and legacy removal.

Minimal fix: either mark partial_implementations explicitly as non-verifier metadata, or add a typed supplemental schema and validate statuses, parent-contained entrypoint IDs, report paths/hashes, legacy removal, and source-freshness limits. Do not treat nested execution summaries as final Evidence.

Code review results

PASS — fixed module and command shape

- app-server-go/internal/parity/commands.go:60-64 maps go-daemon-package-test only to session-daemon-go.
- commands.go:65-82 requires a session-daemon-go path ending in _test.go, a Test* symbol, an internal/ or cmd/ package, and the exact code-owned display template. The executable and arguments are returned separately as [go, test, package, -count=1].
- commands.go:104-108 rejects empty, absolute, colon/backslash-bearing, non-allowlisted, and non-clean paths.
- app-server-go/internal/parity/daemon_command_test.go:8-35 passed and rejects cross-module, traversal, shell suffix, package-template, and test-symbol injection mutations.
- No reviewed call site evaluates the human-readable cd ... && ... metadata through a shell.

PASS — IDs and test names

- All 44 F03 entrypoint IDs are unique within F03 and exist in the frozen baseline.
- Every nested partial entrypoint is contained in the parent F03 entrypoint set.
- All 15 F03 test references match exactly one function definition. The four daemon mappings resolve to:
  - TestGetOperationReadsLegacyWSLedgerOverAuthenticatedTCPAndAfterReopen
  - TestGetOperationPreservesUntypedAndMalformedCompletedBytes
  - TestTCPSlowReaderReleasesBlockedSendWithoutClientCancellation
  - TestLegacyShutdownWriteFailureReleasesAdmission

PASS, bounded — evidence binding

- app-server-go/internal/parity/manifest.go:197-238 re-resolves each mapped test and binds evidence by command ID, path, name, and exact display before validating an execution artifact.
- app-server-go/internal/parity/evidence.go:17-20 correctly says metadata and hashes establish byte integrity, not proof that a command ran. Trusted execution and independent review remain separate gates.
- F03 has no final Evidence records. The new daemon mappings are approved command shapes only.

Ledger trace

Parent F03

- status: IMPLEMENTED
- evidence: empty
- blockers: 3
- broad stage unfinished: true
- legacy removed: false
- no final evidence or full-migration claim

OperationQuery partial

- status: REVIEWED
- independent report: docs/reports/full-go-p2-operation-final-review.json
- report SHA-256: 6ed10ff0d56300b2e02c6559df3350dc342adad6cc1dcc384313733e754be457
- source_freshness_limited: true
- legacy_removed: false
- scratch result exists at C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p2-python-malformed-verified/result.json
- declared and observed result SHA-256: 14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e
- ledger candidate binary hash and result-embedded daemon hash agree at 39ec833a217f7bcf588c67d192f2417ce1cbd8f58e4ba2eca90d304f0cc12c7d
- limitation: the supplemental record has no candidate binary path, so this review did not re-hash the binary bytes

Control/event partial

- status: ISOLATED_VERIFIED
- review status: PASS_SCOPED_PREVIOUS_HASHES
- report: docs/reports/full-go-p2-control-review.json
- report SHA-256: 79da275e484d8b64065219c18bb285afc05017857905b877e7a725b990401844
- legacy_removed: false
- the ledger correctly limits this to the original two-service prior hash set; it is not a current-source or whole-P2 review claim

Commands and exits

- go test ./internal/parity -count=1 && go vet ./internal/parity — exit 0; package passed, no vet diagnostics.
- go test ./internal/parity -run '^TestDaemonTestEvidenceUsesFixedModuleCommand$' -count=1 -v — exit 0; focused test passed.
- final go vet ./internal/parity — exit 0; no diagnostics.
- read-only F03 frozen-ID check — exit 0; 44 IDs, zero missing, zero duplicate.
- read-only F03 test-definition check — exit 0; 15 references, zero missing/duplicate definition.
- initial reviewer-only Python inspection using top-level key items — exit 1 with KeyError; corrected to the actual migration_items schema and succeeded. This was not a product failure.

Source identities

- app-server-go/internal/parity/commands.go — 6bda7a30c8720860b2ba68079f68fe151ddac186378f849b6b5fe070a4dc18b3
- app-server-go/internal/parity/daemon_command_test.go — 3b5156c64e1dae9e3f8851fd9d114732b5d07714ccff001971abb7f5887e7cb7
- docs/reports/full-go-migration-matrix.json — 7bc5d50813daa14925ba79a4b832d7b7f144910fcd72a9ff70740a779f73f8df
- app-server-go/internal/parity/baseline.go — f2852c26f901ac2153a01f86875c822d76f9b13a53944f1b41247836ba5584ae
- app-server-go/internal/parity/manifest.go — ed2d4f98472c5c43a1918ef07d50e5b721f98fed814626cc8c442b902533d1a5
- app-server-go/internal/parity/evidence.go — 85de6900efe270ce5786b89b04f55b622e0265a2f92453764e2702bcf54ca36f
- session-daemon-go/internal/grpcapi/operation.go — b736dde6e322fb68cb960c1e09bab59f90afcbfae4166e5fbfa27a0ca8237661
- session-daemon-go/internal/protocol/operation_facade.go — 7e6b8953bd9d2f0566977c46208c0548eb8a4cd581d7f4317f504fbc69fa0eda
- session-daemon-go/internal/grpcapi/operation_test.go — 94d8cd66a04b5c1ec476126f8759312939efcd29c0084c70621a1d7bb1cfb96a
- session-daemon-go/internal/grpcapi/slow_reader_test.go — f2fea3200f4b6ec52527e7433e856aa980a18f86bbad54100b81cede6ff6e094
- session-daemon-go/internal/protocol/shutdown_write_failure_test.go — 1d9cf9bb152d1d04c5744c4335dd330294471c0c836daff382941801e871e6da

Provenance and limits

HEAD is ff32f947024b1840b939ebfb8e58e45886d34b47. All three scoped files are untracked, so the per-file hashes above—not HEAD alone—identify the reviewed source.

No repository file was edited. No commit, push, stash, install, service, daemon, production system, database, secret, chat, or real inference was touched. No daemon package or broad race suite was run. Full Go migration remains unfinished.
