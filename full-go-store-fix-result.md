# Go Store compatibility and reliability fix result

Generated: 2026-10-08T22:58:50+08:00

Repository: `P:/workspace/glwlg/ai/astrorder`

Source: branch `master`, HEAD `ff32f947024b1840b939ebfb8e58e45886d34b47`. `app-server-go/` is untracked in this worktree, so HEAD alone does not identify these source files; exact changed-file hashes are in `full-go-store-fix-result.json`.

## Outcome

The four source-evidenced Store findings from the independent review are implemented and focused-verified:

- `STORE-HASH-001`: command hashes now match Python `json.dumps(... ensure_ascii=False, sort_keys=True, separators=(",", ":"))` for sorted keys, Unicode, `<>&`, U+2028/U+2029, null, large integers, and distinct integer/float JSON lexemes. The same `payloadHash` path is used by command creation, old-row backfill, and native-identity rehashing. No hash version was added and no ledger was cleared.
- `STORE-UTF8-001`: session upsert/update, task insert/update, connection-history stage/state, and redacted text now truncate by Unicode code point. Session/task titles retain exactly 512 valid code points; redacted text retains exactly 2000.
- `STORE-SCHEMA-001`: validate-only startup compares exact column order/type/nullability/default/primary-key shape, table-level unique identities, and named index uniqueness/partial/column shape. Old-version migrations rebuild tables transactionally after backfills/native-identity migration so upgraded databases reach the canonical constrained schema while preserving rows.
- `STORE-MIGRATION-001`: schema migration has an OS-visible, nonblocking per-database lock (`LockFileEx` on Windows, `flock` on Linux) in addition to the in-process mutex. A real two-process test proves a second migrator fails closed and succeeds after release.

The coordinator-owned typed Projector API was not changed. Parity fixtures and other packages/modules were not edited.

## Independent review used

The pre-fix review decision remains `FAIL`; it has not been rewritten as a post-fix approval, and no independent re-review was run.

- JSON: `C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-store-events-review.json`
  SHA-256: `11015793cf64d27b70a5b816b8ad3e908c4d379806fb40cca476333d64feb399`
- Markdown: `C:/Users/luwei/AppData/Local/hermes/cache/scratch/full-go-store-events-review.md`
  SHA-256: `61a3973e56a82b174b7cae3ef469791b9058a089b13a966b972fd58b6b2dea1a`
- Reviewed-source manifest recorded by that review: `d5d5642ed3977f83b9b6ef3e1e7e48ac2223f3453b4c6ce1de5d67d39c9ced1b`

Review-passed areas were preserved: opaque typed Projector registration, ApplyFrame transaction semantics, explicit native-identity migration without text inference, and preference tombstone/omitted-field behavior.

## Exact compatibility contract and fixtures

The legacy command business payload remains exactly:

`id`, `agent_id`, `session_id`, `action`, `text`, ordered `attachment_ids`, nullable `target_id`.

State, timestamps, error text, and full attachment metadata are not hash fields, matching `backend/src/astrorder/store.py:create_command`.

One-time Python-computed fixture hashes embedded in tests:

- Unicode/HTML/U+2028/U+2029 command: `a97d9db6a153e46238230fbb74ea06f00224ae8397009e68ae4bd01f37a3b0f5`
- Unknown large integer plus `1.0` versus integer `1`: `6132587c59a30dfa0845dfb74e888b845c202d26470dba4dddbb85de3ad06746`
- Old no-hash row backfill: `58146bd8766d6bbcb44710ccd6f8cb8029eab80cb6f2d9333f59c2fba1241b92`
- Native-identity old scope: `ee3fc382a0bde5f6f5cccd2e8608a83d6f2d022fb9b7411f30216199eb9ec4aa`
- Native-identity new scope: `45f238f4a11bc8e222352fcc2920d736062acaf628d15cbefffec3ebc0bac45f`

The retry tests assert same-payload retries return `created=false`; different payloads return `DuplicateCommandError`; and exact row ID, queued state, creation time, payload hash, and unrelated checkpoint remain unchanged. Native-identity migration changes only the expected session scope and corresponding Python-compatible hash.

All databases used by the new tests are private `testing.T` temporary SQLite databases. The only existing database read was the committed non-production parity fixture `app-server-go/internal/parity/testdata/store/store-compat-fixture.sqlite3`. No production/user database, production configuration, credential, secret, chat, or MCP data was read.

## Red/green evidence

Initial focused red command on Windows:

`C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe test ./internal/store -run 'Test(PayloadHashMatchesPythonCanonicalJSON|JSONCodecPreservesUnknownIntegersAndFloatSemantics|ExistingPythonQueuedCommandRetryUsesExactLedgerHash|NativeIdentityMigrationRehashesWithPythonContractAndKeepsLedgerIdentity|UnicodeLimitsPreserveCodepointsAndValidUTF8OnEveryTitlePath)$' -count=1`

Result: `FAIL` as intended. It showed both Python hash fixture mismatches, large integer conversion to `float64`, rejection of an existing Python-hashed queued retry, incompatible native-identity rehashing, and invalid UTF-8 after multibyte title truncation.

Post-fix focused command (same command):

Result: `PASS` — `ok astrorder.dev/app-server/internal/store 0.586s`.

Independent-review follow-up red command:

`C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe test ./internal/store -run 'Test(ValidateSchemaRejectsStructuralDrift|SchemaMigrationOwnershipIsCrossProcess)$' -count=1`

Initial result: `FAIL` because the cross-process lock and canonical table rebuild did not exist. Post-fix result: `PASS` — `ok astrorder.dev/app-server/internal/store 1.176s`.

## Final verification

Windows 11, Go 1.26.4 windows/amd64:

- `C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe test ./internal/store -count=1 -timeout=180s`
  PASS: `ok astrorder.dev/app-server/internal/store 2.240s`
- `C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe test ./internal/preferences -count=1 -timeout=120s`
  PASS: `ok astrorder.dev/app-server/internal/preferences 0.403s`
- `C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe vet ./internal/store`
  PASS
- `C:/Users/luwei/.codex/tools/go1.26.4.windows-amd64/go/bin/go.exe vet ./internal/preferences`
  PASS

Linux WSL, Go 1.26.4 linux/amd64, with caches confined to `/mnt/p/workspace/glwlg/ai/astrorder/.runtime/full-go-linux/{race-cache,modcache,race-tmp}`:

- `../.runtime/full-go-linux/go/bin/go test -race ./internal/store -count=1 -timeout=240s`
  PASS: `ok astrorder.dev/app-server/internal/store 20.899s`
- `../.runtime/full-go-linux/go/bin/go test -race ./internal/preferences -count=1 -timeout=180s`
  PASS: `ok astrorder.dev/app-server/internal/preferences 2.393s`
- `../.runtime/full-go-linux/go/bin/go vet ./internal/store ./internal/preferences`
  PASS

Repository hygiene:

- `git diff --check -- app-server-go/internal/store`
  PASS
- No commit, push, stash, service deployment, App restart, or Session Daemon restart was performed.

## Source locations

- Canonical JSON, exact native-number preservation, hashing, and Unicode truncation: `app-server-go/internal/store/codec.go`
- Session title paths: `app-server-go/internal/store/agents_sessions.go`
- Task title paths and command idempotency: `app-server-go/internal/store/commands_tasks.go`
- Redacted/history text slices: `app-server-go/internal/store/connections_reviews_metrics.go`
- Schema canonicalization/validation: `app-server-go/internal/store/schema.go`
- Migration ownership integration: `app-server-go/internal/store/store.go`
- Platform locks: `app-server-go/internal/store/migration_lock_windows.go`, `migration_lock_unix.go`
- Hash/number/retry/Unicode tests: `app-server-go/internal/store/compatibility_test.go`
- Schema mutation/two-process tests: `app-server-go/internal/store/schema_reliability_test.go`

## Status and remaining work

This is `IMPLEMENTED_AND_FOCUSED_VERIFIED` for the reviewed Store findings only. It does not declare P5 done or the full migration complete.

Remaining:

- An independent post-fix re-review has not run.
- The production App projector is not registered/exercised; T03 integration remains blocked even though isolated ApplyFrame semantics passed review.
- Genuine gRPC transport/projection and later migration stages remain pending.
- No production cutover, App deployment, daemon restart, or real user-data validation was performed.
