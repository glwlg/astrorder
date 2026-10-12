Full-Go P0 frozen-inventory preservation review

Status: FAIL
Passed: false

The canonical frozen baseline is unchanged:

  Path: docs/reports/full-go-baseline.json
  Raw SHA-256: c19a3dda9689fa886a17ba21fc29a0a9f46ee82fc8180235f8b719d6106d4ff0
  Canonical content SHA-256: d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc

Finding FGP0-PRESERVATION-001 — high

Path: scripts/migration/full_go_inventory.py
Line: 1829
Related: backend/tests/test_full_go_inventory.py:141; backend/tests/test_full_go_inventory_dispatch.py:30

Source verification accepts loss of one runtime-specific legacy WebSocket dispatch when another runtime still dispatches the same action.

The frozen baseline records session.reasoning.set dispatch in both Hermes and Grok. In the private fixture I removed the Hermes case while retaining Grok. The scanner then reported:

  frozen_dispatch_sources=session-daemon-go/internal/runtime/hermes/adapter.go,session-daemon-go/internal/runtime/grok/runtime.go
  mutated_dispatch_sources=session-daemon-go/internal/runtime/grok/runtime.go
  support_equal=true
  source_count=2->1

Verification still exited 0:

  verified source inventory against frozen baseline d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc

Why this is a weakening

The reproducibility equality helper removes all daemon dispatch_sources. The final verifier only rejects a formerly supported action when current support is no longer SUPPORTED or no dispatch source remains. It does not reject partial loss of frozen dispatch coverage. The new regression covers complete global support loss, not one missing source from a multi-runtime action.

Minimal fix

Compare stable dispatch coverage while continuing to ignore volatile line numbers and file hashes. Normalize each daemon dispatch source to repository-relative source identity, or preferably an explicit runtime/handler identity, and require every frozen identity to remain represented. Add a regression with one action dispatched by two non-grpcapi sources; remove one and require verify exit 1.

Positive results

  - grpcapi is excluded from the legacy WS scanner, and the focused receipt-projection regression passed in the tested snapshot.
  - Two direct generations were byte-identical. Both embedded canonical SHA-256 847bb8ef078eb265d1f5b4be1939c5c47b8a5b709921bc4ce64216e3bb29b8d4; both raw files had SHA-256 f658790c46a80f9f35432ee4ace26b570895090218e2e1a833fa7ca39e8a07b0.
  - Normalized current versus frozen equality was true after removing only daemon source, dispatch_sources, and content_sha256. IDs, feature/requirement mappings, support, unsupported evidence, auth, callers, contracts, and all non-daemon categories remained exact.
  - Removing the sole dispatch for session.approval.respond failed verification with its missing daemon action ID.
  - The new-entrypoint and tracked-script-deletion fail-closed tests passed in the tested snapshot.

Exact tested exits

  1. Focused four-test pytest command: exit 0; 4 passed, 2 warnings; 74.50s.
  2. Both complete scoped test files: exit 0; 16 passed, 2 warnings; 78.86s.
  3. Direct generation repro-a: exit 0; canonical SHA-256 847bb8ef078eb265d1f5b4be1939c5c47b8a5b709921bc4ce64216e3bb29b8d4.
  4. Direct generation repro-b: exit 0; same canonical SHA-256; raw outputs equal.
  5. Source-aware verification against the canonical frozen baseline: exit 0.
  6. Normalized frozen/current comparison probe: exit 0; normalized_contract_equal=true; 15 daemon citation-only action changes.
  7. Fixture verification after deleting the sole session.approval.respond dispatch: exit 1 as expected; missing daemon action ID reported.
  8. Combined fixture generation attempt: exit 1 because the clone omitted untracked app-server-go/internal/memory/client_test.go; this is a fixture limitation and not product evidence.
  9. Fixture verification after deleting only the Hermes session.reasoning.set dispatch: exit 0 unexpectedly; this reproduces the finding.
  10. Direct build_inventory comparison for that mutation: exit 0; dispatch source count changed 2->1 while support stayed equal.

Final exact scope SHA-256

  scripts/migration/full_go_inventory.py
    5b16e7d21d6f153ef764fd1bacc92e7e9f239d0840b553c4ef5e75f2446fa511

  backend/tests/test_full_go_inventory.py
    09db3be47c5b8a4a9a25f33e3c54458ffb2803dfbd3a1c56350116749715b1a3

  backend/tests/test_full_go_inventory_dispatch.py
    173b7f9e94f53f1625beb9474a6260115900e94b36d35bb6828a364d85df9770

Verification limit

The scanner and dispatch test changed concurrently after the 16-test run. That pass covered earlier hashes (scanner 6a40f1363079d0184d6c7a11171940c1f1bf7afc60172c3e464a06423ef291ca; dispatch test daf904fda79a4a0ae1f9b93ce61dfbc46aff4d42cc3a9e0c041f53d54fdc7354). Final hashes are listed above but did not receive a complete scoped pytest rerun. Failure is therefore preserved independently of the coverage finding.

Reproduction root

  C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-preservation-review-fixtures

No production services, secrets, databases, chat, real inference, commits, pushes, or repository files were changed. Only private scratch fixtures and the requested reports were written. This review does not mark P0 or the overall migration complete.
