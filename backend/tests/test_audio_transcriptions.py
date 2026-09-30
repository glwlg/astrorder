import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from astrorder.api import router
from astrorder.config import Settings


def test_transcription_route_requires_browser_auth():
    app = FastAPI()
    app.state.settings = Settings(browser_secret='test-secret')
    app.state.store = None
    app.include_router(router)
    with TestClient(app) as client:
        response = client.post('/api/v1/audio/transcriptions', files={'file': ('test.wav', b'audio', 'audio/wav')})
    assert response.status_code == 401


@pytest.fixture
def audio_client(monkeypatch):
    from astrorder.routers import audio
    app = FastAPI()
    app.state.settings = Settings(browser_secret='test-secret')
    app.state.store = None
    app.include_router(router)
    config = {'gateway_type': 'opencodex', 'inference_url': 'https://llm.example/v1/', 'api_key': 'private-key'}
    monkeypatch.setattr(audio, 'get_gateway_config', lambda store: config, raising=False)
    with TestClient(app, headers={'Authorization': 'Bearer test-secret'}) as client:
        yield client, config


def test_transcription_uses_configured_gateway_and_returns_only_text(audio_client, monkeypatch):
    from astrorder.routers import audio
    client, _config = audio_client
    captured = []
    def handler(request):
        captured.append(request)
        return httpx.Response(200, json={'text': '测试转写', 'internal': 'not exposed'})
    factory = httpx.AsyncClient
    monkeypatch.setattr(audio, 'upstream_client', lambda: factory(transport=httpx.MockTransport(handler)), raising=False)
    response = client.post('/api/v1/audio/transcriptions', files={'file': ('test.wav', b'audio', 'audio/wav')})
    assert response.status_code == 200
    assert response.json() == {'text': '测试转写'}
    assert str(captured[0].url) == 'https://llm.example/v1/audio/transcriptions'
    assert captured[0].headers['X-OpenCodex-API-Key'] == 'private-key'
    assert b'gpt-4o-transcribe' in captured[0].content
    assert b'audio' in captured[0].content


@pytest.mark.parametrize('body,mime,status', [(b'', 'audio/wav', 400), (b'bad', 'text/html', 415), (b'x' * 11, 'audio/wav', 413)])
def test_rejects_invalid_uploads(audio_client, monkeypatch, body, mime, status):
    from astrorder.routers import audio
    monkeypatch.setattr(audio, 'MAX_AUDIO_BYTES', 10, raising=False)
    client, _ = audio_client
    response = client.post('/api/v1/audio/transcriptions', files={'file': ('audio.wav', body, mime)})
    assert response.status_code == status


@pytest.mark.parametrize('status,payload', [(403, {'error': 'private-key'}), (502, {'error': 'bad'}), (200, {'missing': 'text'}), (200, {'text': 123})])
def test_upstream_errors_do_not_leak_secrets(audio_client, monkeypatch, status, payload):
    from astrorder.routers import audio
    client, _ = audio_client
    factory = httpx.AsyncClient
    monkeypatch.setattr(audio, 'upstream_client', lambda: factory(transport=httpx.MockTransport(lambda req: httpx.Response(status, json=payload))))
    response = client.post('/api/v1/audio/transcriptions', files={'file': ('audio.wav', b'audio', 'audio/wav')})
    assert response.status_code == 502
    assert 'private-key' not in response.text


def test_uses_opencodex_profile_and_environment_key(audio_client, monkeypatch):
    from astrorder.routers import audio
    client, config = audio_client
    config.update(gateway_type='magpie', gateway_profiles={'opencodex': {'inference_url': 'https://voice.example/v1', 'api_key': ''}})
    monkeypatch.setenv('OPENCODEX_API_AUTH_TOKEN', 'env-key')
    captured = []
    def handler(req):
        captured.append(req)
        return httpx.Response(200, json={'text': 'ok'})
    factory = httpx.AsyncClient
    monkeypatch.setattr(audio, 'upstream_client', lambda: factory(transport=httpx.MockTransport(handler)))
    response = client.post('/api/v1/audio/transcriptions', files={'file': ('a.wav', b'audio', 'audio/wav')})
    assert response.status_code == 200
    assert str(captured[0].url) == 'https://voice.example/v1/audio/transcriptions'
    assert captured[0].headers['X-OpenCodex-API-Key'] == 'env-key'


def test_missing_key_is_configuration_error(audio_client, monkeypatch):
    client, config = audio_client
    config['api_key'] = ''
    monkeypatch.delenv('OPENCODEX_API_AUTH_TOKEN', raising=False)
    response = client.post('/api/v1/audio/transcriptions', files={'file': ('a.wav', b'audio', 'audio/wav')})
    assert response.status_code == 503
