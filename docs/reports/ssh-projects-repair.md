# SSH deployment and project/session repair

Status: investigation and RED regression phase

Scope:
- repair remote bootstrap command encoding and remote-shell quoting;
- preserve structured remote deployment diagnostics;
- verify idempotent Astrorder plugin deployment against the configured Hermes runtime;
- reconcile legacy local source/session identities without merging distinct profiles or machines;
- enumerate the configured Hermes profile's complete visible session/project catalog using native APIs and truthful completeness metadata.

Safety boundaries:
- Existing uncommitted work is preserved.
- Hermes model/context/global configuration is not changed (`model.context_length=300000`, `compression.threshold=0.5`).
- No credentials, private keys, tokens, Desktop/Overlook state, Hermes core files, or unrelated remote services are read or changed.
- The saved SSH target is authoritative; placeholders are not used as target values.
- Existing conversations are read-only during discovery; no test prompt is sent to them.
- Astrorder uses ports 30001 (frontend) and 30002 (backend); port 5173 is not used.

Initial verified checkpoint:
- Branch: `master`.
- Current preview listeners exist on `127.0.0.1:30001` and `127.0.0.1:30002`; they were not restarted in this phase.
- The generated SSH install and bridge commands currently pass raw base64 as the `python -c` source; this is the reproduced deployment defect.
- The installed Hermes `session.list` handler ignores caller `offset`, applies hidden/archived filtering through `list_sessions_rich`, and returns no native total. A short page therefore cannot prove completeness.
- The installed Hermes project tree is profile-scoped and includes explicit projects, discovered repositories, and archived-aware project catalog data; the adapter currently assumes only a shallow `projects` session map.

Next checkpoint: RED tests will exercise the generated bootstrap through a real local POSIX shell/interpreter, a legacy SQLite migration fixture, and native pagination/filter/catalog response shapes before production changes are made.
