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
- **Antigravity**: Uses runtime-provided `ANTIGRAVITY_CONVERSATION_ID` and resolves caller binding automatically via sibling `astrorder-caller.json`.

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

### 7. Self-Scheduled Tasks (Session-Bound Timers & Crons)
Any session (whether a regular session or a Bot session) can create, inspect, pause/resume, or delete persistent Astrorder scheduled tasks bound strictly to **its own current session**. When triggered, Astrorder automatically sends `--prompt` back into this session, persists schedules across restarts, and displays them live in the Astrorder UI.

- List scheduled tasks on this session:
  ```bash
  astrorder-agent schedules list
  ```
- Create a one-shot relative timer (e.g. remind/check in 10 minutes) or exact timestamp:
  ```bash
  astrorder-agent schedules create --name "检查部署结果" --in 10m --when-busy queue --prompt "检查发布状态并向用户汇报"
  astrorder-agent schedules create --name "定时提醒" --run-at "2026-11-26T18:00:00+08:00" --prompt "执行预定操作"
  ```
- Create a recurring interval, daily, weekly, or cron schedule:
  ```bash
  astrorder-agent schedules create --name "每30分钟巡检" --interval 30 --when-busy skip --prompt "检查关键指标与告警"
  astrorder-agent schedules create --name "每日早报" --daily 09:00 --timezone Asia/Shanghai --prompt "汇总昨日进展与今日待办"
  astrorder-agent schedules create --name "工作日晨检" --weekly 09:30 --weekdays mon-fri --timezone Asia/Shanghai --prompt "执行工作日例行巡检"
  astrorder-agent schedules create --name "整点巡检" --cron "0 9-18 * * 1-5" --timezone Asia/Shanghai --prompt "执行整点健康检查"
  ```
- Pause, resume, or edit an existing schedule on this session:
  ```bash
  astrorder-agent schedules update --id <SCHEDULE_ID> --enabled false
  astrorder-agent schedules update --id <SCHEDULE_ID> --enabled true --interval 60
  ```
- Delete / cancel a schedule on this session:
  ```bash
  astrorder-agent schedules delete --id <SCHEDULE_ID>
  ```
Optional flags on `create`/`update`: `--prompt-file <PATH>`, `--timezone <IANA_OR_Local>`, `--when-busy <skip|queue>`, `--misfire-policy <grace_once|skip|run_once>`, `--misfire-grace-sec <SECONDS>`. Schedules are strictly scoped to the verified caller session and cannot target another session.

### 8. Bots (Workspace-Backed Domain Bots)
Discover and invoke registered Bots (each Bot owns its dedicated workspace, `AGENTS.md`, and `.agents/skills/`):
- List registered Bots:
  ```bash
  astrorder-agent bots list
  ```
- Invoke a Bot (spawns a clean new job session in the Bot's workspace and optionally copies caller files into the Bot's `inbox/<job-id>/`):
  ```bash
  astrorder-agent bots invoke --bot <BOT_SLUG_OR_NAME> --operation-id <UNIQUE_OP_ID> --title "<JOB_TITLE>" --prompt "<SELF_CONTAINED_BRIEF>" [--attach "<PATH1>,<PATH2>"]
  ```
Give the Bot a concise, self-contained brief and attach key reference files when helpful so it does not have to crawl external projects by default; Astrorder also includes optional caller context pointers so the Bot can still inspect or ask back if it decides more detail is needed. Follow up with `astrorder-agent sessions read --key <BOT_JOB_SESSION_KEY>` until the Bot finishes.

### 9. Extensions & Third-Party Plugins (`ext.*`)
Astrorder hosts built-in and third-party plugins (such as CI/CD tools, issue trackers, or internal integrations) without hardcoding vendor commands into core skills. Discover enabled plugin capabilities first, then invoke them with caller workspace context automatically attached:

- List all capabilities exposed by currently enabled plugins (includes `plugin_id`, `capability_id`, `summary`, and `input_schema`):
  ```bash
  astrorder-agent ext list
  ```
- Invoke any enabled plugin capability:
  ```bash
  astrorder-agent ext call <PLUGIN_ID> <CAPABILITY_ID> [--param key=value ...] [--json '<JSON_OBJECT>']
  ```
When a plugin provides its own skill (e.g. `astrorder-gitlab-ci`), load that skill for plugin-specific workflows and parameter details.

### 10. Learning Radar (Bot-Driven Blind-Spot Discovery & Mastery)
Interact with Astrorder Learning Radar (`成长雷达`) to inspect active blind spots, ingest Mentor Bot scan results, or archive mastered topics after a study session:

- View pending blind spots and due spaced-repetition reviews:
  ```bash
  astrorder-agent learning status [--status open|learning|review_due|mastered]
  ```
- Submit structured scan results from the Mentor Bot:
  ```bash
  astrorder-agent learning ingest --run-id <RUN_ID> --file <RESULT.json>
  ```
- Mark a topic mastered / save takeaways & local Markdown note path:
  ```bash
  astrorder-agent learning update --topic-id <TOPIC_ID> --status mastered --notes "<SUMMARY>" --notes-file "<NOTE_PATH>"
  ```
- Trigger a manual Mentor Bot scan:
  ```bash
  astrorder-agent learning scan [--days 7]
  ```

### 11. Other Capabilities via the Same CLI
Blackboard, swarm coordination, workspace discovery and the monitor room are separate skills, but every one of them calls this same executable with a UTF-8 JSON file:

```bash
astrorder-agent invoke <capability> --input-file "<PATH_TO_INPUT.json>"
```

Use `astrorder-agent catalog` to discover capability ids and their fields. Do not switch to MCP or an inline shell-quoted payload. Load the matching sibling skill (`astrorder-blackboard`, `astrorder-swarm`, `astrorder-workspace`, `astrorder-monitor`, `astrorder-browser`) for its command details.
