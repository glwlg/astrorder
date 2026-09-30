from __future__ import annotations

import json
import struct
import wave
from pathlib import Path

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from astrorder.api import router
from astrorder.config import Settings


class FakeProcess:
    def __init__(self):
        self.pid = 1234
        self.returncode = None
        self.terminated = False
        self.killed = False

    def poll(self):
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = 0

    def kill(self):
        self.killed = True
        self.returncode = -9

    def wait(self, timeout=None):
        return self.returncode


@pytest.fixture
def resource_dir(tmp_path: Path) -> Path:
    (tmp_path / 'runtime').mkdir()
    (tmp_path / 'models').mkdir()
    (tmp_path / 'runtime' / 'llama-server.exe').write_bytes(b'exe')
    (tmp_path / 'models' / 'Confucius4-R2T2-Q8_0.gguf').write_bytes(b'model')
    (tmp_path / 'models' / 'mmproj-Confucius4-R2T2-Q8_0.gguf').write_bytes(b'mmproj')
    return tmp_path


def test_plugin_enable_starts_server_and_disable_unloads_model(resource_dir, monkeypatch):
    from astrorder.plugins.r2t2 import LocalR2T2Plugin

    process = FakeProcess()
    monkeypatch.setattr(LocalR2T2Plugin, '_find_free_port', lambda self: 32123)
    monkeypatch.setattr(LocalR2T2Plugin, '_wait_until_ready', lambda self: None)
    plugin = LocalR2T2Plugin(None, resource_dir=resource_dir, process_factory=lambda *args, **kwargs: process)

    plugin.enable(wait=True)
    status = plugin.status()
    assert status['enabled'] is True
    assert status['state'] == 'running'
    assert status['pid'] == 1234
    assert status['device'] == 'Vulkan0'

    plugin.disable()
    assert process.terminated is True
    assert plugin.status()['state'] == 'disabled'


def test_plugin_transcribes_pcm_through_persistent_server(resource_dir, monkeypatch):
    from astrorder.plugins.r2t2 import LocalR2T2Plugin

    process = FakeProcess()
    monkeypatch.setattr(LocalR2T2Plugin, '_find_free_port', lambda self: 32123)
    monkeypatch.setattr(LocalR2T2Plugin, '_wait_until_ready', lambda self: None)
    plugin = LocalR2T2Plugin(None, resource_dir=resource_dir, process_factory=lambda *args, **kwargs: process)
    plugin.enable(wait=True)
    captured = []

    def handler(request: httpx.Request):
        captured.append(json.loads(request.content))
        return httpx.Response(200, json={
            'choices': [{'message': {'content': 'language Chinese<asr_text>本地流式听写。'}}],
        })

    monkeypatch.setattr(plugin, '_http_transport', httpx.MockTransport(handler))
    pcm = struct.pack('<' + 'h' * 1600, *([100] * 1600))
    assert plugin.transcribe_pcm(pcm) == '本地流式听写。'
    audio = captured[0]['messages'][0]['content'][1]['input_audio']['data']
    import base64
    with wave.open(__import__('io').BytesIO(base64.b64decode(audio))) as wav:
        assert wav.getframerate() == 16000
        assert wav.getnchannels() == 1
        assert wav.getsampwidth() == 2


def test_plugin_api_toggles_runtime_and_reports_status():
    class Plugin:
        def __init__(self):
            self.enabled = False
        def status(self):
            return {'enabled': self.enabled, 'state': 'running' if self.enabled else 'disabled'}
        def enable(self):
            self.enabled = True
        def disable(self):
            self.enabled = False

    app = FastAPI()
    app.state.settings = Settings(browser_secret='test-secret')
    app.state.store = None
    app.state.local_voice = Plugin()
    app.include_router(router)
    with TestClient(app, headers={'Authorization': 'Bearer test-secret'}) as client:
        response = client.put('/api/v1/audio/local-plugin', json={'enabled': True})
        assert response.status_code == 200
        assert response.json()['enabled'] is True
        readback = client.get('/api/v1/audio/local-plugin')
        assert readback.json()['state'] == 'running'
        response = client.put('/api/v1/audio/local-plugin', json={'enabled': False})
        assert response.json() == {'enabled': False, 'state': 'disabled'}


def test_stream_websocket_requires_running_plugin():
    class Plugin:
        def status(self):
            return {'enabled': False, 'state': 'disabled'}

    app = FastAPI()
    app.state.settings = Settings(browser_secret='test-secret')
    app.state.store = None
    app.state.local_voice = Plugin()
    app.include_router(router)
    with TestClient(app) as client:
        with client.websocket_connect('/api/v1/audio/local-stream?token=test-secret') as websocket:
            assert websocket.receive_json() == {
                'type': 'error',
                'message': 'R2T2 本地流式听写插件未运行。',
            }


def test_stream_websocket_returns_incremental_transcripts():
    class Plugin:
        def status(self):
            return {'enabled': True, 'state': 'running'}
        def transcribe_pcm(self, pcm):
            return '甚至出现交易。' if len(pcm) < 64000 else '甚至出现交易几乎停滞的情况。'

    app = FastAPI()
    app.state.settings = Settings(browser_secret='test-secret')
    app.state.store = None
    app.state.local_voice = Plugin()
    app.include_router(router)
    with TestClient(app) as client:
        with client.websocket_connect('/api/v1/audio/local-stream?token=test-secret') as websocket:
            assert websocket.receive_json()['type'] == 'ready'
            websocket.send_bytes(b'\x00' * 32000)
            assert websocket.receive_json() == {'type': 'transcript', 'text': '甚至出现交易。'}
            websocket.send_bytes(b'\x00' * 32000)
            assert websocket.receive_json() == {'type': 'transcript', 'text': '甚至出现交易几乎停滞的情况。'}
            websocket.send_text('{"type":"stop"}')
            assert websocket.receive_json() == {'type': 'done', 'text': '甚至出现交易几乎停滞的情况。'}
