"""Private native authority fixture: no model calls or user-profile writes."""
import asyncio
import base64
import json
import os
from pathlib import Path
import sys
from types import SimpleNamespace


async def main():
    home = Path(os.environ['HERMES_HOME'])
    home.mkdir(parents=True, exist_ok=True)
    from gateway.config import GatewayConfig, Platform
    from gateway.session import SessionSource, SessionStore
    from gateway.session_authority import LiveSession, initialize_session_authority
    from gateway.session_contract import Principal, SessionRef, Submission
    from gateway.session_images import _stage
    from hermes_state_runtime import list_session_admissions

    source = SessionSource(platform=Platform.TELEGRAM, chat_id='fixture')
    store = SessionStore(home / 'sessions', GatewayConfig())
    seen = []
    expected = None

    async def answer(event):
        assert event.media_types == ['image/png']
        assert len(event.media_urls) == 1
        data = Path(event.media_urls[0]).read_bytes()
        assert data == expected
        seen.append({'text': event.text, 'bytes': len(data), 'media_types': event.media_types})
        return 'fixture completed'

    runner = SimpleNamespace(
        _session_db=store._db, session_store=store, _draining=False,
        _handle_message=answer, _adapter_for_source=lambda source: None)
    authority = await initialize_session_authority(
        runner, profile_id='fixture', instance_id='fixture-owner')
    store._db.create_session('native-handle', source='telegram')
    authority.sessions['native-handle'] = LiveSession(source, 'native-handle')
    actor = Principal('human', 'fixture', frozenset({'session:submit'}), 'fixture-token')
    for line in sys.stdin:
        req = json.loads(line)
        params = req['params']
        method = req['method']
        if method == 'image.attach_bytes':
            expected = base64.b64decode(params['content_base64'])
            result = _stage(home / 'cache' / 'images', params)
        elif method == 'prompt.submit':
            assert params['attachments'], 'Go adapter omitted canonical images'
            receipt = await authority.submit(actor, Submission(
                params['submission_id'], SessionRef('fixture', 'native-handle'),
                {'text': params['text'], 'attachments': params['attachments']}, 'queue'))
            assert receipt.status == 'queued'
            await authority.sessions['native-handle'].task
            assert len(seen) == 1, 'media never reached native handler'
            admissions = list_session_admissions(
                authority.db, session_id='native-handle', pending_only=False)
            assert len(admissions) == 1 and admissions[0]['status'] == 'terminal'
            assert admissions[0]['payload']['attachments_v1']['media_types'] == ['image/png']
            result = {'status': 'streaming', 'fixture_media_verified': True}
        else:
            raise AssertionError('unexpected RPC ' + method)
        print(json.dumps({'id': req['id'], 'result': result}), flush=True)
    print(json.dumps({'ok': True, 'native_media': seen}), file=sys.stderr)


asyncio.run(main())
