Full Go foundation addendum final preferences review

Verdict: PASS_BOUNDED_PREF_TX_002_FIX

PREF-TX-002 is resolved in the pinned snapshot. This third independent review was limited to the complete-document readback fix, its new SQLite regression, and focused Windows/WSL verification. It did not review or make claims about the active gRPC ledger/query, shutdown, native-payload, or blocked-send work.

Finding resolution

preferences.go retains json.Decoder.UseNumber and now performs a second decode after reading the stored value. Only io.EOF is accepted. A second JSON value, trailing non-whitespace text, or another token therefore makes readback fail.

That failure propagates out of readPreferences through the Store.WithTx callback. Store.WithTx rolls back whenever the callback returns an error, so Apply and Import cannot commit their writes when any scanned shared-table value is not exactly one complete JSON document.

The new TestMalformedStoredJSONRollsBackWholePreferenceWrite uses a real temporary SQLite Store. It inserts an unrelated malformed shared-table row and covers:

- {} {}
- {} trailing
- null false

For each malformed document it exercises both Apply and Import. Each operation attempts three writes—session_pins:one, session_pins:two, and pinned_projects—requires an error, and verifies that the count of those three rows remains zero.

No security concern, logic error, or blocking suggestion was found in the scoped two-file delta.

Verification

- Windows package test: exit 0, preferences passed in 0.948s.
- Windows vet: exit 0, no diagnostics.
- WSL Linux race package test: exit 0, preferences passed in 9.700s. The Go timeout was 60 seconds and the outer command timeout was 180 seconds, so the earlier printed-pass/outer-124 ambiguity is gone.
- Windows focused trailing-JSON regression: all three subtests passed; package 0.652s.
- WSL focused trailing-JSON race regression: all three subtests passed; package 3.112s.

Historical RED note

The coordinator records that, before the second-decode fix, all three candidate updates committed for Apply and Import. This reviewer did not revert or edit source to recreate that historical RED. The completed snapshot and its GREEN behavior were independently inspected and executed.

Pinned source

- app-server-go/internal/preferences/preferences.go: dbeb333dbb171de66bf08de0051e7c49aa6774d28f79219f5590694ed07f3539
- app-server-go/internal/preferences/trailing_json_test.go: 87c09dace5c49bba3bb669c40795cde98d80669ba477046ae34958d66f6b32cc
- Branch: master
- HEAD and origin/master: ff32f947024b1840b939ebfb8e58e45886d34b47

The scoped source hashes remained stable during review. The preference files remain untracked worktree content.

Boundaries retained

This PASS closes only PREF-TX-002 in the bounded preferences domain snapshot. The preferences handler remains unmounted and its route/key ownership remains with the Python stage. No claim is made about P2 gRPC work, shutdown changes, native payloads, blocked sends, full migration, release, installation, deployment, or production readiness.

The original addendum FAIL and the PREF-TX-002 rereview FAIL remain preserved as historical reports. The retained foundation report is unchanged. No repository file, commit, push, stash, daemon, service, production/kernel system, real credential, real database/chat, or profile was modified or contacted; no additional agent was spawned.
