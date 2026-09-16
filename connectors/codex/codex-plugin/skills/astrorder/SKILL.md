---
name: astrorder
description: Use the Astrorder MCP server to read sessions, list agents and machines, and call Astrorder capabilities. Use when a message contains an Astrorder session key or the user asks about other sessions, agents, or machines.
---

Astrorder is available as the MCP server named `astrorder`. Use those MCP tools only. Do not call Astrorder over HTTP or CLI.

Tools:

- catalog_list
- sessions_list (compact session metadata, default limit 30)
- sessions_search (search sessions by keywords with limit)
- sessions_read (read message history of a specific session, default limit 20, supports 'before' cursor for older pages)
- sessions_create (create a new session under an agent: agent_id, optional title and workspace)
- sessions_send (send prompt instruction to a session: key, text)
- sessions_stop (stop/interrupt a running session: key)
- agents_list
- machines_list
- projects_list
- plugins_list (list Astrorder UI/artifact plugins and status: drawio, mermaid, excalidraw, diff, three, html, terminal, monaco, etc.)
- plugins_configure (toggle or configure an Astrorder plugin: plugin_id, optional enabled, optional config)
- plugins_open (open a specific plugin or artifact viewer in user UI: plugin_id, optional path, url, title, session_key)
- plugins_close (close a plugin tab or collapse the sidecar panel: optional plugin_id, tab_id, collapse)

When a message includes a session key like `agent_id::session_id`, call `sessions_read` with `{ "key": "agent_id::session_id" }`. It returns the latest page of messages, `count`, `has_more`, and `next_cursor`. If you need earlier history, pass `before: next_cursor`.
Prefer using `sessions_read` directly when you have a session key. If looking for a session, prefer `sessions_search` over `sessions_list` to save tokens.

`catalog_list` is the live menu. New Astrorder capabilities show up there.
