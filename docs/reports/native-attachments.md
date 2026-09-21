# Native attachments

## Delivered

- Hermes outbound attachments use native `image.attach_bytes` for images and `file.attach` data URLs for other files. Controller filesystem paths are never sent to local or SSH runtimes. Failed attach aborts before `prompt.submit`; queued images are detached if submit is not confirmed.
- Hermes effective capabilities now include `attachments` when a native command channel is registered. Composer attach control follows that capability.
- Codex outbound attachments accept images and audio as inline data URLs. Other files use their original desktop path when it is already readable under the session policy, otherwise they are staged under the session workspace and sent as native file mentions. Remote Codex files are staged on the remote workspace before submission.
- Inbound native images are copied into Astrorder storage only when the file sits under a native root (Hermes profile/composer dirs, Codex home/workspace). Foreign paths stay as filename cards with no preview. Browser still does not fetch arbitrary local files.
- Remote Codex can read `localImage` bytes through existing SSH stdin, still constrained to the reported Codex home.

## Verification

- Backend complete suite: 133 passed.
- Frontend: 124 tests across 52 files passed.
- Isolated browser protocol checks run after the production candidate build.

## Not claimed

- No real native model turn with an image or audio payload.
- Codex reads staged documents through its workspace tools; this is distinct from the Responses API `input_file` transport.
- Hermes native queue is still not the Astrorder outbox.
- Permanent deletion of a disposable native thread is reported separately if executed.
