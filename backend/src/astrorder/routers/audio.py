"""Browser-authenticated gateway and local-plugin dictation endpoints."""
import asyncio
import json
import os
from typing import Annotated

import httpx
from fastapi import APIRouter, File, HTTPException, Request, UploadFile, WebSocket, WebSocketDisconnect
from pydantic import BaseModel

from astrorder.core.auth import authorize_browser_websocket, require_browser
from astrorder.core.gateway_config import get_gateway_config

router = APIRouter(prefix='/api/v1/audio', tags=['audio'])
MAX_AUDIO_BYTES = 25 * 1024 * 1024
AUDIO_TYPES = {'audio/wav', 'audio/x-wav', 'audio/webm', 'audio/ogg',
               'audio/mp4', 'audio/mpeg', 'audio/aac', 'audio/flac', 'audio/x-m4a'}
MAX_STREAM_BYTES = 16000 * 2 * 60 * 10


class LocalPluginUpdate(BaseModel):
    enabled: bool


def upstream_client():
    return httpx.AsyncClient(timeout=120, follow_redirects=False)


@router.post('/transcriptions')
async def transcribe(request: Request, file: Annotated[UploadFile, File()]):
    require_browser(request, request.app.state.settings)
    mime = (file.content_type or '').split(';', 1)[0].lower()
    if mime not in AUDIO_TYPES:
        raise HTTPException(415, '不支持此音频格式。')
    data = await file.read(MAX_AUDIO_BYTES + 1)
    if not data:
        raise HTTPException(400, '录音为空，请重新录音。')
    if len(data) > MAX_AUDIO_BYTES:
        raise HTTPException(413, '音频不能超过 25 MB。')
    config = get_gateway_config(request.app.state.store)
    profile = config if config.get('gateway_type') == 'opencodex' else config.get('gateway_profiles', {}).get('opencodex', {})
    key = profile.get('api_key') or os.environ.get('OPENCODEX_API_AUTH_TOKEN', '')
    base = profile.get('inference_url', '').rstrip('/')
    if not key or not base:
        raise HTTPException(503, '请先在网关设置中配置 OpenCodex 推理地址和数据密钥。')
    try:
        async with upstream_client() as client:
            response = await client.post(
                base + '/audio/transcriptions',
                headers={'X-OpenCodex-API-Key': key},
                data={'model': 'gpt-4o-transcribe'},
                files={'file': (file.filename or 'recording.wav', data, mime)},
            )
    except httpx.TimeoutException:
        raise HTTPException(504, '语音转写超时，请重试。') from None
    except httpx.HTTPError:
        raise HTTPException(502, '无法连接语音网关，请检查网络和网关配置。') from None
    if response.status_code != 200:
        raise HTTPException(502, f'语音网关返回 HTTP {response.status_code}，请检查网关权限或稍后重试。')
    try:
        payload = response.json()
    except ValueError:
        payload = None
    if not isinstance(payload, dict) or not isinstance(payload.get('text'), str):
        raise HTTPException(502, '语音网关未返回有效的转写文本。')
    return {'text': payload['text']}


@router.get('/local-plugin')
async def local_plugin_status(request: Request):
    require_browser(request, request.app.state.settings)
    return request.app.state.local_voice.status()


@router.put('/local-plugin')
async def update_local_plugin(request: Request, update: LocalPluginUpdate):
    require_browser(request, request.app.state.settings)
    plugin = request.app.state.local_voice
    if update.enabled:
        plugin.enable()
    else:
        await asyncio.to_thread(plugin.disable)
    return plugin.status()


@router.websocket('/local-stream')
async def local_stream(websocket: WebSocket):
    if not authorize_browser_websocket(websocket, websocket.app.state.settings):
        await websocket.close(code=1008)
        return
    await websocket.accept()
    plugin = websocket.app.state.local_voice
    if plugin.status().get('state') != 'running':
        await websocket.send_json({'type': 'error', 'message': 'R2T2 本地流式听写插件未运行。'})
        await websocket.close(code=1013)
        return
    await websocket.send_json({'type': 'ready', 'sample_rate': 16000})
    pcm = bytearray()
    transcript = ''
    changed = asyncio.Event()
    stopping = False

    async def infer():
        nonlocal transcript
        last_inference_size = 0
        while True:
            await changed.wait()
            changed.clear()
            size = len(pcm)
            if size > last_inference_size and (stopping or size - last_inference_size >= 32000):
                transcript = await asyncio.to_thread(plugin.transcribe_pcm, bytes(pcm))
                last_inference_size = size
                await websocket.send_json({'type': 'transcript', 'text': transcript})
            if stopping and len(pcm) == last_inference_size:
                await websocket.send_json({'type': 'done', 'text': transcript})
                await websocket.close(code=1000)
                return

    async def guarded_infer():
        try:
            await infer()
        except Exception:
            await websocket.send_json({'type': 'error', 'message': '本地流式听写失败，请重试。'})
            await websocket.close(code=1011)

    inference = asyncio.create_task(guarded_infer())
    try:
        while True:
            message = await websocket.receive()
            if message.get('type') == 'websocket.disconnect':
                return
            chunk = message.get('bytes')
            if chunk:
                pcm.extend(chunk)
                if len(pcm) > MAX_STREAM_BYTES:
                    await websocket.send_json({'type': 'error', 'message': '单次流式听写不能超过 10 分钟。'})
                    await websocket.close(code=1009)
                    return
                changed.set()
                continue
            text = message.get('text')
            if not text:
                continue
            try:
                control = json.loads(text)
            except ValueError:
                continue
            if not isinstance(control, dict):
                continue
            if control.get('type') == 'cancel':
                await websocket.send_json({'type': 'cancelled'})
                await websocket.close(code=1000)
                return
            if control.get('type') == 'stop':
                stopping = True
                changed.set()
                await inference
                return
    except WebSocketDisconnect:
        return
    except Exception:
        await websocket.send_json({'type': 'error', 'message': '本地流式听写失败，请重试。'})
        await websocket.close(code=1011)
    finally:
        inference.cancel()
        await asyncio.gather(inference, return_exceptions=True)
