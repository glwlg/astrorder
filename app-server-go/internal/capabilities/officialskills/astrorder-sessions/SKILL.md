---
name: astrorder-sessions
description: Use when managing or delegating Astrorder sessions.
---

# Astrorder Session Management & Swarm Collaboration

Astrorder provides autonomous multi-agent session lifecycle management and delegation via the native CLI `astrorder-agent`.

## Executable Location & Caller Identity

Use this installed skill's verified sibling executable: `<skill_dir>/scripts/astrorder-agent.exe` on Windows or `<skill_dir>/scripts/astrorder-agent` on Unix. Resolve `<skill_dir>` from the Agent's skill loader, never from cwd or an assumed PATH. In commands below, `astrorder-agent` means that exact executable. Call it directly in the terminal, without Python/subprocess wrappers; execute spawn and read in separate terminal calls. Session commands use CLI, not MCP.
Caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`) is resolved by the runtime:
- **Hermes**: Automatically discovers `<home>/astrorder-caller.json` and uses `HERMES_SESSION_ID`.
- **Codex**: Uses `CODEX_THREAD_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Grok**: Uses runtime-provided `GROK_SESSION_ID` and caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`

Session IDs come dynamically from the runtime. Do not read, print, set, forward, guess, or persist session-ID variables; let the CLI resolve and verify the caller. The four-field caller file is static identity configuration, not authentication. Never copy another Agent's identity or credentials. Missing configuration is an error; do not choose identity by recency, workspace, or the only active session.

## Session Lifecycle Commands

### 1. Discover Agents & Sessions
- Discover available agents:
  ```bash
  astrorder-agent agents
  ```
- List visible active sessions:
  ```bash
  astrorder-agent sessions list
  ```
- Search sessions by query:
  ```bash
  astrorder-agent sessions search "<query>"
  ```

### 2. Spawn Sub-Task (Delegation)
Delegate a focused sub-task to an autonomous agent session:
```bash
astrorder-agent sessions spawn --operation-id <UNIQUE_OP_ID> --agent-id <TARGET_AGENT> --prompt "<INSTRUCTION>"
```
Optional flags:
- `--title "<TITLE>"`: Human-readable session title.
- `--workspace "<PATH>"`: Working directory for target agent.
- `--prompt-file "<PATH>"`: Path to file containing long prompt.
- `--model <MODEL>`, `--provider <PROVIDER>`, `--effort <EFFORT>`: Explicit model preferences when needed.

For the same configured Agent, omitting workspace inherits the verified caller workspace. For a different Agent, specify a path valid on its machine. Give each child the goal, constraints, and expected output; let it load its own skills. Do not put CLI commands, executable paths, caller identity, or credentials into child prompts. Fork, handoff, and derivation alone are not collaboration relationships.

On timeout or disconnection, retry with exactly the same operation ID. Never create a new ID while the previous outcome is uncertain. Consult `--help` before adding options not described here.

### 3. Read Session Messages
Read conversation history and responses from a child session:
```bash
astrorder-agent sessions read --key <KEY>
```
Spawn returns the child in `session`; use `session.key` (or `session.agent_id + "::" + session.id`). Read returns messages in `items` with `role` and `text`; keep reading until the actual child final assistant reply arrives. An accepted receipt or operation stage `completed` only acknowledges dispatch, not model reasoning completion. A marker quoted in a user prompt or tool output is not an assistant result. Use `--limit <N>` and `--before <CURSOR>` to paginate; do not probe nonexistent or unrelated keys to learn the format.

### 4. Send Message to Session
Send follow-up instructions to an existing session:
```bash
astrorder-agent sessions send --key <KEY> --text "<INSTRUCTION>"
```

### 5. Swarm Tree & Topology Inspection
Inspect parent-child session hierarchy and swarm execution graph:
```bash
astrorder-agent sessions tree [--key <ROOT_KEY>]
```

### 6. Interrupt or Delete Session
- Interrupt a running turn:
  ```bash
  astrorder-agent sessions stop --key <KEY>
  ```
- Clean up runtime resources:
  ```bash
  astrorder-agent sessions delete --key <KEY>
  ```

Interrupt or delete only within the user's authorized scope. Keep exact composite keys intact; never target unrelated working sessions or restart a runtime to make an operation succeed.

### 7. Other Capabilities via the Same CLI
Blackboard, swarm coordination, workspace discovery and the monitor room are separate skills, but every one of them calls this same executable with a UTF-8 JSON file:

```bash
astrorder-agent invoke <capability> --input-file "<PATH_TO_INPUT.json>"
```

Use `astrorder-agent catalog` to discover capability ids and their fields. Do not switch to MCP or an inline shell-quoted payload. Load the matching sibling skill (`astrorder-blackboard`, `astrorder-swarm`, `astrorder-workspace`, `astrorder-monitor`, `astrorder-browser`) for its command details.
