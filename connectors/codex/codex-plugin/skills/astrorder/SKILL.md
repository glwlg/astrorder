---
name: astrorder
description: Use the Astrorder MCP server to read sessions, list agents and machines, and call Astrorder capabilities. Use when a message contains an Astrorder session key or the user asks about other sessions, agents, or machines.
---

Astrorder is available as the MCP server named `astrorder`. ALWAYS call the Astrorder MCP tools directly (e.g. `mcp:astrorder.machines_dispatch`, `mcp:astrorder.sessions_create`, `mcp:astrorder.plugins_open`, `mcp:astrorder.blackboard_set`).
NEVER write custom Python scripts or execute curl/bash/terminal commands to simulate or call Astrorder APIs. Use the native MCP tool calls provided to you.

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
- browser_run (run a bounded browser task through Astrorder: url, goal, optional exact field-label inputs and max_steps)
- machines_dispatch (dispatch a task or delegate session creation onto a specific target machine/host: machine_id, optional agent_kind, title, workspace, prompt)
- monitor_sessions_add (add session keys into Astrorder Monitor room dashboard for live visual tracking: key or keys)
- monitor_sessions_remove (remove a session from Monitor room dashboard: key)
- monitor_layout_set (configure Monitor room grid layout columns: columns 1-4)
- blackboard_get (read shared mission state or API specifications from the blackboard: optional key, optional namespace)
- blackboard_set (publish shared mission state, API specifications or parameters to the blackboard: key, value, optional namespace).
  * Generative UI Support: Astrorder automatically renders rich visual interactive UI for human operators on the Blackboard when you provide values matching structured schemas:
    - StepTimeline: { title: string, steps: [{ title: string, status: 'completed'|'running'|'pending'|'failed', description?: string, time?: string }] }
    - MetricGrid: { title: string, metrics: [{ label: string, value: string|number, unit?: string, change?: string, status?: 'good'|'warn'|'bad' }] }
    - ApiEndpointsCard: { title: string, baseUrl?: string, endpoints: [{ method: 'GET'|'POST'|'PUT'|'DELETE', path: string, desc?: string, status?: number }] }
    - ResourceUsageBar: { title: string, resources: [{ name: string, used?: number, total?: number, unit?: string, percent?: number }] }
    - TestReport: { title: string, passed: number, failed: number, skipped?: number, duration?: string, cases?: [{ name: string, status: 'passed'|'failed', duration?: string }] }
    - CveSecurityReport: { title: string, critical: number, high: number, medium: number, low: number, vulnerabilities?: [{ cve: string, package: string, severity: 'CRITICAL'|'HIGH'|'MEDIUM' }] }
    - DiffViewer: { file: string, diff: string }
    - Checklist: { title: string, items: [{ label: string, done: boolean, assignee?: string }] }
    - TerminalLog: { title: string, lines: string[], status?: 'ok'|'error' }
    - ArchitectureFlow: { title: string, nodes: [{ name: string, role: string, status?: 'ok'|'warn', desc?: string }] }
    - StatusCard: { title: string, status: 'success'|'running'|'ready'|'warning'|'error'|'info', summary?: string, details?: object }
    - Or explicit json-render spec: { type: '<ComponentName>', props: { ... } }
- blackboard_delete (remove a key from the blackboard: key, optional namespace)
- swarm_milestone_declare (declare dependency gate or expected milestone, optional auto-wake session key & prompt)
- swarm_milestone_resolve (mark milestone resolved, automatically waking any blocked worker sessions)
- swarm_milestone_list (list all swarm milestones and resolution status)
- swarm_telemetry_report (report structured execution progress: progress 0-100, phase, status 'ok'/'warning'/'blocked'/'completed', summary, artifacts)
- swarm_telemetry_get (read worker telemetry progress reports without loading full chat histories: optional key)
- swarm_sos_escalate (escalate a blocker, fatal conflict, or failure to the queen orchestrator: reason, optional context, key, target_session_key)
- swarm_sos_list (list active unresolved SOS alerts across the swarm)
- swarm_sos_resolve (resolve an SOS alert: sos_id, optional resolution)
- swarm_lock_acquire (acquire exclusive lock on shared resource like 'git:repo' or 'db:migration': resource, optional ttl_seconds, owner_key)
- swarm_lock_release (release exclusive resource lock: resource, optional owner_key)
- swarm_lock_list (list currently active non-expired resource locks)

When a message includes a session key like `agent_id::session_id`, call `sessions_read` with `{ "key": "agent_id::session_id" }`. It returns the latest page of messages, `count`, `has_more`, and `next_cursor`. If you need earlier history, pass `before: next_cursor`.
Prefer using `sessions_read` directly when you have a session key. If looking for a session, prefer `sessions_search` over `sessions_list` to save tokens.

`catalog_list` is the live menu. New Astrorder capabilities show up there.
