---
name: astrorder-gitlab-ci
description: Use when checking GitLab CI/CD pipeline status, stage/job progress, or reading failed CI job logs for the current workspace repository via Astrorder.
---

# Astrorder GitLab CI Plugin

Inspect self-hosted or cloud GitLab CI/CD pipelines, stage/job breakdowns, and ANSI-cleaned failure logs directly through Astrorder without managing personal access tokens or manual curl requests.

Astrorder automatically resolves the matching configured GitLab instance, repository path, branch, and HEAD commit from the calling session's workspace `.git/config` and `HEAD`.

## Executable Location & Caller Identity

Use this installed skill's verified sibling executable: `<skill_dir>/scripts/astrorder-agent.exe` on Windows or `<skill_dir>/scripts/astrorder-agent` on Unix. Resolve `<skill_dir>` from the Agent's skill loader, never from cwd or an assumed PATH. In commands below, `astrorder-agent` means that exact executable.

Caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`) is resolved by the runtime:
- **Hermes**: Automatically discovers `<home>/astrorder-caller.json` and uses `HERMES_SESSION_ID`.
- **Codex**: Uses `CODEX_THREAD_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Grok**: Uses runtime-provided `GROK_SESSION_ID` and caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`

## Commands

### 1. Inspect Latest Pipeline Stages & Jobs (Default for Current HEAD/Branch)
```bash
astrorder-agent ext call gitlab-ci-viewer pipeline
# Or specify a pipeline ID / branch:
astrorder-agent ext call gitlab-ci-viewer pipeline --param pipeline_id=12345
astrorder-agent ext call gitlab-ci-viewer pipeline --param ref=main
```

### 2. List Recent CI Pipelines
```bash
astrorder-agent ext call gitlab-ci-viewer pipelines --param limit=10
astrorder-agent ext call gitlab-ci-viewer pipelines --param ref=main --param limit=5
```

### 3. Read Failed or Running Job Logs (ANSI-Cleaned Tail)
```bash
astrorder-agent ext call gitlab-ci-viewer job_log --param job_id=67890 --param tail=200
```

### Shortcut Alias
The equivalent shortcut subcommands remain supported:
```bash
astrorder-agent gitlab status [--pipeline-id <ID>] [--ref <BRANCH>]
astrorder-agent gitlab pipelines [--ref <BRANCH>] [--limit 10]
astrorder-agent gitlab log --job-id <JOB_ID> [--tail 200]
```

Optional overrides for any command: `--param project=<GROUP/REPO>`, `--param instance_id=<INSTANCE_ID_OR_NAME>`, `--param workspace=<WORKSPACE_PATH>`.
