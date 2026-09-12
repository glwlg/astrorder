"""Single-worker production entrypoint; machine settings stay outside Git."""
from __future__ import annotations

import ctypes
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONFIG = ROOT / '.runtime' / 'production.json'


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


def main():
    import uvicorn

    from astrorder.config import Settings


    config = json.loads(CONFIG.read_text(encoding='utf-8'))
    environment = config['environment']
    for key, value in environment.items():
        if not key.startswith('ASTRORDER_') or key.endswith('SECRET'):
            raise ValueError('Invalid public production setting')
        os.environ[key] = str(value)
    encrypted = ROOT / '.runtime' / 'production.credentials.dpapi'
    credentials = json.loads(protect_bytes(encrypted.read_bytes(), decrypt=True))
    for key in required_credential_keys(environment):
        if not credentials.get(key):
            raise ValueError('Production credential missing')
        os.environ[key] = credentials[key]
    if _enabled(environment.get('ASTRORDER_SESSION_DAEMON_ENABLED')):
        from production_daemon import ensure_production_daemon

        ensure_production_daemon(environment, root=ROOT)
    os.chdir(ROOT)
    settings = Settings.from_env()
    from astrorder.main import create_app
    if not settings.static_dir or not (settings.static_dir / 'index.html').is_file():
        raise RuntimeError('Build frontend assets before starting production')
    uvicorn.run(create_app(settings), host=settings.host, port=settings.port, workers=1, reload=False, access_log=False)


if __name__ == '__main__':
    main()
