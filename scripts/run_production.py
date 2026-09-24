"""Single-worker production entrypoint; machine settings stay outside Git."""
from __future__ import annotations

import ctypes
import json
import os
import sys
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))


def durable_root() -> Path:
    local_app_data = os.environ.get('LOCALAPPDATA')
    if not local_app_data:
        raise RuntimeError('LOCALAPPDATA is unavailable')
    return Path(local_app_data) / 'Astrorder'


CONFIG = durable_root() / 'production.json'
CREDENTIALS = durable_root() / 'production.credentials.dpapi'


def _enabled(value: object) -> bool:
    return str(value or '').strip().casefold() in {'1', 'true', 'yes', 'on'}


def required_credential_keys(environment: dict[str, object]) -> tuple[str, ...]:
    keys = ['ASTRORDER_BROWSER_SECRET', 'ASTRORDER_CONNECTOR_SECRET']
    if _enabled(environment.get('ASTRORDER_SESSION_DAEMON_ENABLED')):
        keys.append('ASTRORDER_SESSION_DAEMON_SECRET')
    return tuple(keys)


def protect_bytes(value: bytes, *, decrypt: bool = False) -> bytes:
    """Windows current-user DPAPI; never persist plaintext service credentials."""
    class Blob(ctypes.Structure):
        _fields_ = [('size', ctypes.c_ulong), ('data', ctypes.POINTER(ctypes.c_ubyte))]
    buffer = ctypes.create_string_buffer(value)
    source = Blob(len(value), ctypes.cast(buffer, ctypes.POINTER(ctypes.c_ubyte)))
    output = Blob()
    operation = ctypes.windll.crypt32.CryptUnprotectData if decrypt else ctypes.windll.crypt32.CryptProtectData
    if not operation(ctypes.byref(source), None, None, None, None, 1, ctypes.byref(output)):
        raise ctypes.WinError()
    try:
        return ctypes.string_at(output.data, output.size)
    finally:
        ctypes.windll.kernel32.LocalFree(output.data)


def load_production_environment() -> dict[str, str]:
    config = json.loads(CONFIG.read_text(encoding='utf-8'))
    environment = config['environment']
    for key, value in environment.items():
        if not key.startswith('ASTRORDER_') or key.endswith('SECRET'):
            raise ValueError('Invalid public production setting')
        os.environ[key] = str(value)
    credentials = json.loads(protect_bytes(CREDENTIALS.read_bytes(), decrypt=True))
    for key in required_credential_keys(environment):
        if not credentials.get(key):
            raise ValueError('Production credential missing')
        os.environ[key] = credentials[key]
    return {key: str(value) for key, value in environment.items()}


def stop_when_desktop_exits(server, raw_pid: str | None) -> None:
    if raw_pid is None:
        return
    try:
        pid = int(raw_pid)
    except ValueError as exc:
        raise ValueError('ASTRORDER_DESKTOP_PID must be an integer') from exc
    if pid <= 0:
        raise ValueError('ASTRORDER_DESKTOP_PID must be positive')

    def watch() -> None:
        synchronize = 0x00100000
        handle = ctypes.windll.kernel32.OpenProcess(synchronize, False, pid)
        if handle:
            try:
                ctypes.windll.kernel32.WaitForSingleObject(handle, 0xFFFFFFFF)
            finally:
                ctypes.windll.kernel32.CloseHandle(handle)
        server.should_exit = True

    threading.Thread(target=watch, name='desktop-lifetime', daemon=True).start()


def main():
    import uvicorn

    from astrorder.config import Settings


    environment = load_production_environment()
    if _enabled(environment.get('ASTRORDER_SESSION_DAEMON_ENABLED')):
        from production_daemon import ensure_production_daemon

        ensure_production_daemon(environment, root=ROOT)
    os.chdir(ROOT)
    settings = Settings.from_env()
    from astrorder.main import create_app
    if not settings.static_dir or not (settings.static_dir / 'index.html').is_file():
        raise RuntimeError('Build frontend assets before starting production')
    application = create_app(settings)
    server = uvicorn.Server(
        uvicorn.Config(
            application,
            host=settings.host,
            port=settings.port,
            workers=1,
            reload=False,
            access_log=False,
        )
    )

    application.state.desktop_shutdown = lambda: setattr(server, "should_exit", True)

    stop_when_desktop_exits(server, os.environ.get('ASTRORDER_DESKTOP_PID'))
    # Windows ProactorEventLoop 优雅退出补丁：修复关闭服务时正在接受握手的 socket 触发 AssertionError
    import sys
    if sys.platform == "win32":
        import asyncio.proactor_events
        orig_init = asyncio.proactor_events._ProactorSocketTransport.__init__
        def patched_init(self, loop, sock, protocol, waiter=None, extra=None, server=None):
            if server is not None and getattr(server, "_sockets", None) is None:
                server = None
            orig_init(self, loop, sock, protocol, waiter, extra, server)
        asyncio.proactor_events._ProactorSocketTransport.__init__ = patched_init

    server.run()


if __name__ == '__main__':
    main()
