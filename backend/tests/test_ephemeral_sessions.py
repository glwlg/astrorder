"""Temporary-session projection regressions; no native processes or user DBs."""
from uuid import uuid4

import pytest

from astrorder.config import Settings
from astrorder.schemas import SessionModel
from astrorder.store import Store


def row(**overrides):
    return {
        'id': 'child', 'agent_id': 'fixture-agent', 'title': 'Renamed side chat',
        'status': 'idle', 'updated_at': '2026-09-11T00:00:00Z', **overrides,
    }


@pytest.mark.parametrize('writer', ['native', 'connector'])
def test_ephemeral_flag_survives_writes_sync_and_reopen(tmp_path, writer):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/fixture.db')
    store = Store(settings)

    def write(data):
        if writer == 'native':
            return store.upsert_session(data)
        data = SessionModel.model_validate(data).model_dump()
        return store.apply_connector_event(
            event_id=str(uuid4()), event_type='session.upsert',
            agent_id=data['agent_id'], session_id=data['id'], data=data,
        )['data']

    assert write(row(ephemeral=True))['ephemeral'] is True
    # Native catalog/status updates may omit or default the field. They must
    # never promote an already temporary session into a normal conversation.
    assert write(row(status='running'))['ephemeral'] is True
    assert write(row(ephemeral=False))['ephemeral'] is True
    assert write(row(id='normal'))['ephemeral'] is False
    assert write(row(agent_id='other-agent', ephemeral=False))['ephemeral'] is False
    store.engine.dispose()
    reopened = Store(settings)
    try:
        assert reopened.get_session('fixture-agent', 'child')['ephemeral'] is True
        assert next(s for s in reopened.list_sessions() if s['id'] == 'normal')['ephemeral'] is False
    finally:
        reopened.engine.dispose()


def test_upgrade_marks_only_exact_legacy_sidechat_and_survives_rename(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/legacy.db')
    store = Store(settings)
    store.upsert_session(row(title='[侧边聊天]'))
    store.upsert_session(row(id='normal', title='修复[侧边聊天]功能'))
    with store.engine.begin() as db:
        db.exec_driver_sql('ALTER TABLE sessions DROP COLUMN ephemeral')
    store.engine.dispose()
    reopened = Store(settings)
    try:
        assert reopened.get_session('fixture-agent', 'child')['ephemeral'] is True
        assert reopened.upsert_session(row(title='Native auto-title'))['ephemeral'] is True
        assert reopened.get_session('fixture-agent', 'normal')['ephemeral'] is False
    finally:
        reopened.engine.dispose()
