---
name: astrorder-desktop-control
description: Use when inspecting or automating native desktop applications and OS GUI on the host computer via Astrorder (screenshots, UI accessibility tree snapshots, clicks, typing, keyboard shortcuts, window switching, clipboard). Prefer CLI/APIs or astrorder-browser for web pages before falling back to desktop GUI control.
---

# Astrorder Desktop Control Plugin

Inspect and operate native desktop applications on the Astrorder host machine through a platform-abstracted GUI automation driver (powered by `CursorTouch/Windows-MCP` on Windows). Works transparently even when your agent runs inside WSL or over SSH.

## When to Use (Capability Priority Ladder)

1. **CLI / File / API first**: Always prefer shell commands, file editing, or domain plugins when available.
2. **Browser pages -> `astrorder-browser`**: For web applications, prefer `astrorder-browser` over OS-level clicking.
3. **Native desktop apps -> `astrorder-desktop-control`**: Use this skill for desktop software (ERP/IM/IDE/Office/dialogs), visual verification, or cross-app GUI workflows.
4. **UIA Snapshot + Label first, Coordinates second**: Start with `snapshot` to read the accessibility tree and element `label` numbers. Prefer `--param label=<N>` over raw `--param loc=x,y` whenever a label is available.

## Executable Location & Caller Identity

Use this installed skill's verified sibling executable: `<skill_dir>/scripts/astrorder-agent.exe` on Windows or `<skill_dir>/scripts/astrorder-agent` on Unix. Resolve `<skill_dir>` from the Agent's skill loader, never from cwd or an assumed PATH. In commands below, `astrorder-agent` means that exact executable.

Caller identity binding (`agent_id`, `connection_id`, `profile_name`, `runtime`) is resolved by the runtime:
- **Antigravity**: Uses `ANTIGRAVITY_CONVERSATION_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Hermes**: Automatically discovers `<home>/astrorder-caller.json` and uses `HERMES_SESSION_ID`.
- **Codex**: Uses `CODEX_THREAD_ID` and resolves caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`
- **Grok**: Uses runtime-provided `GROK_SESSION_ID` and caller binding via `ASTRORDER_CALLER_CONFIG`.
  - Bash: `export ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH}}"`
  - PowerShell: `$env:ASTRORDER_CALLER_CONFIG="{{CALLER_CONFIG_PATH_WIN}}"`

## Observation Commands (Safe in Read-Only Mode)

```bash
# Check platform, driver version, mode (interactive / read_only), and blocked shortcuts
astrorder-agent ext call desktop-control status

# List monitors, bounds, resolution, DPI, and scale
astrorder-agent ext call desktop-control displays

# Capture accessibility UI tree + interactive element labels (optionally with annotated screenshot)
astrorder-agent ext call desktop-control snapshot
astrorder-agent ext call desktop-control snapshot --param use_vision=true --param use_annotation=true

# Capture clean or annotated screenshot (saved automatically to a local file path!)
astrorder-agent ext call desktop-control screenshot
astrorder-agent ext call desktop-control screenshot --param use_annotation=true -o /tmp/desktop.png
```

**Viewing Screenshots**: Whenever `screenshot` or `snapshot --param use_vision=true` returns images, `astrorder-agent` automatically writes each image to disk and replaces base64 data with `images[i].saved_path`. Inspect the image with your file-reading/vision tool on `saved_path`.

## Interaction Commands (Requires `interactive` Mode)

```bash
# Launch, switch to, or resize a window
astrorder-agent ext call desktop-control app --param mode=switch --param name="Notepad"
astrorder-agent ext call desktop-control app --param mode=launch --param name="Calculator"
astrorder-agent ext call desktop-control app --param mode=resize --param name="Notepad" --param window_loc=100,100 --param window_size=1200,800

# Click by UI element label (preferred) or coordinates [x,y]
astrorder-agent ext call desktop-control click --param label=12
astrorder-agent ext call desktop-control click --param loc=640,480 --param button=left --param clicks=1

# Type text into an element or coordinate (optionally clear or press Enter)
astrorder-agent ext call desktop-control type --param label=15 --param text="hello world" --param clear=true --param press_enter=true

# Scroll vertically or horizontally
astrorder-agent ext call desktop-control scroll --param label=8 --param direction=down --param wheel_times=3

# Hover or drag mouse
astrorder-agent ext call desktop-control move --param loc=500,300
astrorder-agent ext call desktop-control move --param drag=true --param from_loc=200,200 --param loc=600,400

# Send keyboard shortcut (dangerous combos blocked by Astrorder policy)
astrorder-agent ext call desktop-control shortcut --param shortcut=ctrl+s

# Wait fixed seconds or poll for a UI condition
astrorder-agent ext call desktop-control wait --param duration=2
astrorder-agent ext call desktop-control wait --param condition=text_exists --param text="Saved" --param timeout=10

# Read or write clipboard
astrorder-agent ext call desktop-control clipboard --param mode=get
astrorder-agent ext call desktop-control clipboard --param mode=set --param text="copied text"
```

## Safety Guidelines

- Take a fresh `snapshot` before clicking or typing after any window transition so element `label` IDs are up to date.
- Before triggering irreversible actions (sending messages/emails, deleting files, submitting payments/forms, closing unsaved work), verify the target element visually and confirm with the user if intent is ambiguous.
- If the plugin is set to `read_only` mode, only observation commands succeed; ask the user to switch mode in Astrorder **设置 → 功能与插件 → 电脑控制** if interaction is required.
