---
name: astrorder
description: Use when delegating tasks or reading Astrorder sessions.
---

# Astrorder Autonomous Swarm Collaboration

Astrorder provides the native CLI `astrorder-agent` for autonomous multi-agent delegation and session inspection. Install it with `astrorderctl agent-skill install`. Browser, blackboard, swarm coordination, workspace and monitor capabilities use the same CLI and can be distributed as independent skills from the capability center.

## Executable Location & Terminal Execution

Locate the binary strictly relative to this skill directory (from `skill_view` output directory `<skill_dir>`, never assume cwd or PATH):
- Windows: `<skill_dir>/scripts/astrorder-agent.exe`
- Unix: `<skill_dir>/scripts/astrorder-agent`

Call the executable directly in `terminal`, for example `"<skill_dir>/scripts/astrorder-agent.exe" agents` on Windows. Do not wrap CLI calls in Python, subprocess scripts, or other programs. Use separate terminal calls for spawn and read; consult a command's `--help` before adding options not shown here. Do not probe nonexistent keys or unrelated sessions to learn the response format. If the sibling executable is absent, use the installed skill directory produced by `astrorderctl agent-skill install`.

## Caller Identity Binding

`astrorder-caller.json` configures caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`), NOT auth credentials (handled via local DPAPI / env tokens). Session IDs are dynamically derived by runtime — NEVER persist session IDs to disk.
- **Hermes**: Discovers `<home>/astrorder-caller.json` and `HERMES_SESSION_ID` automatically. Run CLI directly.
  Do not read, print, set, or forward runtime session-ID variables or inspect caller files to identify yourself. The CLI resolves and validates the caller; choose only the target Agent from `agents`.
- **Codex**: Does not support fixed-path discovery. Set caller config before running CLI:
  - Bash: `export ASTRORDER_CALLER_CONFIG="<home>/astrorder-caller.json"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="<home>\astrorder-caller.json"`
  (Codex supplies `CODEX_THREAD_ID` automatically; never enter session IDs manually.)

## Core Collaboration Workflow

1. **Discover Agents & Capabilities**:
   - `<cli> invoke agents.list` or `<cli> catalog`

2. **Spawn Autonomous Sub-Task**:
   - Standard fork/handoff is not swarm collaboration; allow external parent delegation.
   - Run: `<cli> sessions spawn --operation-id <UNIQUE_OP_ID> --agent-id <TARGET_AGENT> --model group/luozhi --provider magpie --effort max --prompt "<INSTRUCTION>"`
   - Optional parameters: `--title <TITLE>`, `--workspace <PATH>`, `--prompt-file <PATH>`.
   - When workspace is omitted for the same configured Agent, the server inherits the verified caller workspace. For a different Agent, choose an explicit path valid on its machine.
   - Pass the task goal, constraints, and expected result to the child. Let each child discover and load its own installed skill; do not include CLI commands, scripts, executable paths, or caller identity in child prompts.

3. **Verify Execution (Accepted/Operation != Task Completed)**:
   - Receipt `"status": "accepted"` or operation stage `completed` only means creation and dispatch transaction succeeded, NOT that the model finished reasoning.
   - Always verify task completion and outputs by reading child session messages:
     `<cli> sessions read --key <KEY>` (or `--agent-id <ID> --session-id <ID>`)
   - Spawn returns the child in `session`; its key is `session.key` (or `session.agent_id + "::" + session.id`). Read returns messages in `items`, with `role` and `text`; wait and read again until the child's actual final reply arrives. Never treat a marker quoted in a user prompt or tool output as a final assistant reply.

4. **Idempotency & Replay Rules**:
   - On network timeout or disconnection, re-send with the EXACT SAME `--operation-id` (idempotent replay).
   - Under uncertain outcomes, NEVER automatically generate a new operation ID; inspect session state first.

5. **Session Management Commands**:
   - `sessions read --key <KEY> [--limit <N>] [--before <CURSOR>]`: read messages.
   - `sessions send --key <KEY> --text "<INSTRUCTION>"`: send follow-up prompt.
   - `sessions stop --key <KEY>`: stop or interrupt a running session turn.
   - `sessions delete --key <KEY>`: delete session and clean up runtime resources.
   - `sessions tree [--key <KEY>]`: query swarm hierarchy and DAG topology.
   - `sessions list` (no options) / `sessions search <QUERY> [--limit <N>]`: discover and search sessions when needed for the user's task.
   - `invoke <CAPABILITY> --input '<JSON>'`: invoke arbitrary Astrorder capabilities.
   - `invoke <CAPABILITY> --input-file <PATH>`: read a UTF-8 JSON object without shell quoting or command-line length limits.

## Other Capabilities
Use `catalog` to discover capability IDs and fields. For browser JavaScript use `browser eval --script-file <PATH>` with a UTF-8 JavaScript file. CLI calls carry the destination's own runtime identity; never copy another Agent's caller binding or guess an active session. Structured blackboard cards, browser tabs and live UI events are supported through the CLI HTTP transport.
