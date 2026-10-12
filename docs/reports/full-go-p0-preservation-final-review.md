Full-Go P0 preservation fix rereview

Status: PASS
Passed: true

Bounded conclusion

FGP0-PRESERVATION-001 is resolved on the final stable scoped source. There are no open findings in this rereview. This does not mark P0 or the full Go migration goal complete.

Resolved finding: FGP0-PRESERVATION-001 — high

Locations:

  scripts/migration/full_go_inventory.py:705
  scripts/migration/full_go_inventory.py:743
  scripts/migration/full_go_inventory.py:1843
  backend/tests/test_full_go_inventory.py:141
  backend/tests/test_full_go_inventory_dispatch.py:48
  backend/tests/test_full_go_inventory_dispatch.py:75

The verifier now compares frozen versus current repository-relative dispatch source path identities per daemon action. A frozen dispatch path missing from current dispatch_sources returns exit 1 even when another runtime keeps the action globally SUPPORTED. Line numbers and source hashes remain citation data and are intentionally ignored.

The reproducibility normalization preserves action.source.path and replaces dispatch_sources with sorted unique repository-relative paths. It therefore permits line/hash-only citation drift without erasing runtime source identity.

Reproduction and positive checks

The frozen session.reasoning.set dispatch paths are:

  session-daemon-go/internal/runtime/grok/runtime.go
  session-daemon-go/internal/runtime/hermes/adapter.go

The actual repository source verified unchanged with exit 0:

  verified source inventory against frozen baseline d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc

The final real-copy regression creates a scratch Git-backed checkout, copies the actual scoped source, first requires unchanged-source verification to return 0, then removes only this Hermes branch:

  session.reasoning.set -> handleReasoningSet

Grok remains detected, so global support remains present. The mutated verification returns exit 1 with:

  frozen daemon dispatch missing: session.reasoning.set session-daemon-go/internal/runtime/hermes/adapter.go

The citation-only branch changes both dispatch lines by +100 and replaces both sha256 values with 64 zeroes while preserving both paths. It returns 0 and passed in the final scoped suite.

Exact commands and exits

1. Initial full scoped pytest on a concurrently changing snapshot:

  backend/.venv/Scripts/python.exe -m pytest backend/tests/test_full_go_inventory.py backend/tests/test_full_go_inventory_dispatch.py -q --tb=short --basetemp C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-partial-loss-rereview-fixtures

  Exit: 1
  Result: 19 passed, 1 failed, 2 warnings in 97.94s.
  Disposition: not accepted as final evidence. The real-copy fixture lacked Git history, so verification returned 1 earlier for unrelated missing frozen script/build entries. The test was concurrently updated to use a local shared Git clone and to verify unchanged copied source first.

2. Exploratory Git-backed probe using the wrong import context:

  git init . && git fetch --no-tags 'P:/workspace/glwlg/ai/astrorder' ff32f947024b1840b939ebfb8e58e45886d34b47 && PYTHONPATH='<fixture>/backend/src' 'P:/workspace/glwlg/ai/astrorder/backend/.venv/Scripts/python.exe' 'P:/workspace/glwlg/ai/astrorder/scripts/migration/full_go_inventory.py' verify --root '<fixture>' --baseline 'P:/workspace/glwlg/ai/astrorder/docs/reports/full-go-baseline.json'

  Exit: 1
  Result: stopped on unmapped decorator entrypoints due to the wrong import context.
  Disposition: not accepted as product evidence.

3. Helper-compatible manual partial-loss reproduction:

  PYTHONPATH='P:/workspace/glwlg/ai/astrorder/backend/src' 'P:/workspace/glwlg/ai/astrorder/backend/.venv/Scripts/python.exe' 'P:/workspace/glwlg/ai/astrorder/scripts/migration/full_go_inventory.py' verify --root 'C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-partial-loss-rereview-fixtures/test_source_verify_rejects_rem0/checkout' --baseline 'P:/workspace/glwlg/ai/astrorder/docs/reports/full-go-baseline.json'

  Exit: 1
  Result: frozen daemon dispatch missing: session.reasoning.set session-daemon-go/internal/runtime/hermes/adapter.go

4. Final full scoped pytest on the stable final hashes:

  backend/.venv/Scripts/python.exe -m pytest backend/tests/test_full_go_inventory.py backend/tests/test_full_go_inventory_dispatch.py -q --tb=short --basetemp C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-partial-loss-rereview-fixtures

  Exit: 0
  Result: 20 passed, 2 warnings in 112.88s. The warnings were FastAPI/Starlette dependency deprecations.

5. Final scoped Ruff:

  backend/.venv/Scripts/python.exe -m ruff check scripts/migration/full_go_inventory.py backend/tests/test_full_go_inventory.py backend/tests/test_full_go_inventory_dispatch.py

  Exit: 0
  Result: All checks passed!

6. Final unchanged actual-source verification:

  PYTHONPATH='P:/workspace/glwlg/ai/astrorder/backend/src' backend/.venv/Scripts/python.exe scripts/migration/full_go_inventory.py verify --root 'P:/workspace/glwlg/ai/astrorder' --baseline 'P:/workspace/glwlg/ai/astrorder/docs/reports/full-go-baseline.json'

  Exit: 0
  Result: verified source inventory against frozen baseline d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc

Final stable SHA-256

  scripts/migration/full_go_inventory.py
    aae33a665459d129ac53ff43e280b42195837b478de55792c1993f5388404519

  backend/tests/test_full_go_inventory.py
    112ce455b1b5642cb8c61f1bc2db164e56225641bc543ac7ae41b022cb0515d2

  backend/tests/test_full_go_inventory_dispatch.py
    b94bbaeff5e60b865d98970fb936ab6b01de08d4643c79093ba56e777e3a2384

  docs/reports/full-go-baseline.json
    c19a3dda9689fa886a17ba21fc29a0a9f46ee82fc8180235f8b719d6106d4ff0

Baseline canonical content SHA-256:

  d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc

The final scoped hashes were identical immediately before and after the final passing pytest, Ruff, and source verification.

Prior FAIL preservation

The original reports were not overwritten:

  C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-preservation-review.json
    f48f8e9cfaf4caebb11c410090e58cafe7864c7382b36f0007c1ff99b36577b6

  C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-p0-preservation-review.md
    8adbd3e43f62fd2941e3ebf9e0189e41217569a82dac1f6ec7a5e676baa819e8

No repository file was edited by this reviewer. No commit, push, stash, production/service/database/chat/credential contact, or real inference occurred. Writes were limited to the authorized private scratch fixture tree and these final scratch reports.
