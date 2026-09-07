# Model routing verified

Provider: ocx. Live catalog /v1/models was queried during bootstrap; both exact IDs `gpt-5.6-luna` and `gpt-5.6-terra` were present and advertised reasoning effort `max`.

Execution flags are per-session only; no default model/config changes.

- Astrorder 前端开发: gpt-5.6-luna / max
- Astrorder 后端开发: gpt-5.6-luna / max
- Astrorder 全栈联调: gpt-5.6-terra / max, created only after both workers exit with handoff reports

The local launcher serializes integration after concurrent development and records process/session IDs and stages. Successful agent exit alone is never interpreted as a passing test suite. Exact provider execution cannot be independently inferred from historical session titles; use launch arguments and live startup model confirmation.
