# Full-Go migration Python dependency inventory

Source revision: `ff32f947024b1840b939ebfb8e58e45886d34b47`
Frozen inventory SHA-256: `d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc`

This is a dependency inventory, not a migration-completion claim. Every App runtime dependency below remains `NOT_STARTED` until its owning F01–F16 item is implemented and verified in Go. Python used only by development-time inventory/tests is explicitly non-runtime.

## App runtime package dependencies

- `fastapi`
- `uvicorn[standard]`
- `sqlalchemy`
- `pydantic-settings`
- `aiosqlite`
- `python-multipart`
- `httpx`
- `websockets`
- `astrorder-codex-connector`
- `pywinpty>=3.0.5`
- `pywin32>=311; sys_platform == 'win32'`
- `browser-harness==0.1.13`
- `jev-ultrafast`
- `tomlkit>=0.15.1`
- `pyyaml>=6.0.3`

Notable runtime boundaries that require native Go replacements or protocol-equivalent external calls:

- FastAPI/Starlette/Uvicorn: App HTTP and WebSocket server (F01/F06/F09).
- SQLAlchemy: App schema, migrations and all product persistence (F02).
- httpx/websockets/python-multipart: external HTTP, daemon IPC, uploads and streaming (F03/F07/F08/F11/F14).
- jev-ultrafast/browser-harness: App-side browser automation (F13); cannot remain a Python sidecar in final delivery.
- psutil/pywinpty: process and local terminal integration (F05/F06/F14/F16).
- cryptography: SSH and encrypted configuration paths (F05/F16).

## Remote or subprocess Python execution sites

- `backend/src/astrorder/api.py:614` — `from .ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/api.py:658` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/api.py:1767` — `from .ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/api.py:1786` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/core/capabilities_dispatch.py:149` — `f"python3 -c \\\""`
- `backend/src/astrorder/core/capabilities_sync.py:109` — `cmd = f"python3 -c \"{remote_script}\""`
- `backend/src/astrorder/core/capabilities_sync.py:523` — `code, out = _run_cmd_in_target(target, f"python3 -c \"{script}\"", timeout=15)`
- `backend/src/astrorder/core/environment_connections.py:27` — `build_remote_python_command,`
- `backend/src/astrorder/core/environment_connections.py:28` — `build_remote_stdin_bootstrap_command,`
- `backend/src/astrorder/core/environment_connections.py:204` — `launch_argv=self.ssh_argv() + [build_remote_python_command(source)],`
- `backend/src/astrorder/core/ov_sync.py:45` — `["bash", "-c", cmd] if os.name != "nt" else ["python", "-c", cmd],`
- `backend/src/astrorder/core/review_relay.py:67` — `from astrorder.ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/core/review_relay.py:79` — `argv + [build_remote_python_command(script)], capture_output=True, text=True,`
- `backend/src/astrorder/core/session_usage.py:16` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/core/session_usage.py:326` — `cmd = build_remote_python_command(script)`
- `backend/src/astrorder/core/session_usage.py:422` — `cmd = build_remote_python_command(script)`
- `backend/src/astrorder/core/session_usage.py:658` — `cmd = build_remote_python_command(script)`
- `backend/src/astrorder/core/staging_files.py:120` — `from astrorder.ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/core/staging_files.py:150` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/main.py:53` — `sys.executable,`
- `backend/src/astrorder/native/observers.py:389` — `if hasattr(client,'remote_json'):`
- `backend/src/astrorder/native/observers.py:390` — `source=inspect.getsource(observer_plugin)+'\nimport sys\nprint(json.dumps(install_plugin('+repr(home)+',sys.executable,'+repr(content)+','+repr(client._executable())+','+repr(agent_id)+')))'`
- `backend/src/astrorder/native/observers.py:391` — `result=client.remote_json(source)`
- `backend/src/astrorder/observers/io.py:24` — `argv=[str(python),str(script)]`
- `backend/src/astrorder/routers/files.py:66` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/routers/files.py:113` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/routers/files.py:249` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/routers/files.py:285` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/routers/files.py:381` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/routers/files.py:399` — `argv + [build_remote_python_command(script)],`
- `backend/src/astrorder/routers/files.py:444` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/routers/files.py:458` — `argv + [build_remote_python_command(script)],`
- `backend/src/astrorder/routers/files.py:503` — `from ..ssh_transport import SshNativeRuntime, build_remote_python_command`
- `backend/src/astrorder/routers/files.py:521` — `cmd = build_remote_python_command(remote_script)`
- `backend/src/astrorder/ssh_transport.py:268` — `python_command = payload.get("python_command")`
- `backend/src/astrorder/ssh_transport.py:307` — `def build_remote_python_command(`
- `backend/src/astrorder/ssh_transport.py:308` — `source: str, *, interpreter: str = "python3", fallback_interpreter: str | None = "python"`
- `backend/src/astrorder/ssh_transport.py:313` — ```python -c`` source therefore only asks Python to parse the encoded bytes.  The wrapper is`
- `backend/src/astrorder/ssh_transport.py:337` — `def build_remote_stdin_bootstrap_command(`
- `backend/src/astrorder/ssh_transport.py:338` — `*, interpreter: str = "python3", fallback_interpreter: str | None = "python"`
- `backend/src/astrorder/ssh_transport.py:595` — `if not python_command:`
- `backend/src/astrorder/ssh_transport.py:596` — `python_command = (python_path,)`
- `backend/src/astrorder/ssh_transport.py:717` — `build_remote_stdin_bootstrap_command(`
- `backend/src/astrorder/ssh_transport.py:718` — `interpreter=metadata.python_path,`
- `backend/src/astrorder/ssh_transport.py:875` — `command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)`
- `backend/src/astrorder/ssh_transport.py:892` — `command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)`
- `backend/src/astrorder/ssh_transport.py:915` — `command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)`

These entries are classified `MIGRATE_TO_GO_OR_VERSIONED_GO_HELPER`. Native Hermes may retain its own Python environment, but the Astrorder control plane, remote business helpers, build, installer and verification path may not rely on the legacy Python App.

## Development-only Python allowance

- `scripts/migration/full_go_inventory.py` scans the baseline during development.
- `backend/tests/test_full_go_inventory.py` verifies deterministic generation and fail-closed behavior.
- `scripts/migration/full_go_verify.go` is the ultimate verifier and consumes `docs/reports/full-go-baseline.json` after Python App source removal.
