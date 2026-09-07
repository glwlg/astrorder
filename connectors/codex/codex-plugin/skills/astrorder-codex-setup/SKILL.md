---
name: astrorder-codex-setup
description: Configure the Astrorder Codex app-server companion without using CLI transcript scraping.
---

Use the separately installed `astrorder-codex-connector` companion with explicit environment variables. The companion speaks the documented Codex app-server JSON-RPC protocol over its stdio transport and the Astrorder connector WebSocket. It does not submit a prompt through the Codex terminal UI.

Do not put connector secrets in this skill or in a repository file.
