---
name: astrorder-browser
description: Use when controlling the Astrorder browser.
---

# Astrorder Browser Automation

Astrorder provides browser control capabilities enabling autonomous and interactive browsing in an integrated browser tab via the native CLI `astrorder-agent`.

## Executable Location & Caller Identity

Use this installed skill's verified sibling executable: `<skill_dir>/scripts/astrorder-agent.exe` on Windows or `<skill_dir>/scripts/astrorder-agent` on Unix. Resolve `<skill_dir>` from the Agent's skill loader, never from cwd or an assumed PATH. In commands below, `astrorder-agent` means that exact executable. Browser operations use CLI, not MCP.

Official skill distribution automatically installs the destination-platform CLI, caller binding, and private connection configuration. Run the sibling executable directly: it reads the service URL, authentication and caller path from its own installation. Do not ask the user to copy tokens or install the CLI first. If the host address or authentication changes, redistribute the skill to refresh the configuration. Explicit environment overrides take precedence; stale overrides should be removed.

Caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`) is resolved by the runtime:
- **Hermes**: Automatically discovers `<home>/astrorder-caller.json` and uses `HERMES_SESSION_ID`.
- **Codex**: Uses `CODEX_THREAD_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Grok**: Uses runtime-provided `GROK_SESSION_ID` and caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`

Session IDs come dynamically from the runtime. Do not read, print, set, forward, guess, or persist session-ID variables; let the CLI resolve and verify the caller. Never guess session identity from recency, workspace, or another active session.

## Browser Commands

### 1. Evaluate JavaScript (`browser eval`)
Evaluate JavaScript code directly in this session's browser tab using a UTF-8 script file:
```bash
astrorder-agent browser eval --script-file "<PATH_TO_SCRIPT.js>"
```
The script is evaluated via Chrome DevTools Protocol `Runtime.evaluate` on this session's tab.

### 2. Chrome DevTools Protocol (`browser.cdp`)
Execute raw CDP commands using an input JSON file:
```bash
astrorder-agent invoke browser.cdp --input-file "<PATH_TO_INPUT.json>"
```
Example `input.json`:
```json
{
  "method": "Runtime.evaluate",
  "params": {
    "expression": "document.title",
    "returnByValue": true
  }
}
```

### 3. Run Autonomous Browser Task (`browser.run`)
Run a bounded browsing task in this session's visible tab:
```bash
astrorder-agent invoke browser.run --input-file "<PATH_TO_INPUT.json>"
```
Example `input.json`:
```json
{
  "url": "https://example.com",
  "goal": "Read visible content and confirm page title",
  "max_steps": 30,
  "include_screenshot": true
}
```

### 4. Capture Screenshot (`browser.screenshot`)
Capture the current visible tab viewport as an image:
```bash
astrorder-agent invoke browser.screenshot --input-file "<PATH_TO_INPUT.json>"
```
Example `input.json`:
```json
{}
```

## Best Practices
- Prefer `browser eval --script-file <path>` for DOM querying and data extraction.
- For complex navigation and form workflows, use `browser.run`.
- Verify visual outcomes using `browser.screenshot`.
- Respect user authorization; never print passwords, tokens, or security codes.
