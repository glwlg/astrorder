from unittest.mock import Mock

from astrorder.service import ControlService


def test_effective_capabilities_include_live_native_channels_without_mutating_plugin():
    service=ControlService(Mock(),Mock(),Mock())
    agent={'id':'local-claude','kind':'claude-code','capabilities':['chat','events','task_events'],'limitation':'old plugin text'}
    service.register_native_command_handler(
        agent['id'], Mock(), capabilities={'stop', 'attachments'},
        limitation='停止仅作用于当前运行时。图片由原生接口发送。',
    )
    service._native_history_handlers[agent['id']]=Mock()
    effective=service.effective_agent(agent)
    assert {'chat','history','stop','task_events','attachments'}.issubset(effective['capabilities'])
    assert 'approvals' not in effective['capabilities']
    assert agent['capabilities']==['chat','events','task_events']
    assert '当前运行时' in effective['limitation']
    assert '图片' in effective['limitation']
    service.clear_native_command_handler(agent['id']); service._native_history_handlers.clear()
    assert 'stop' not in service.effective_agent(agent)['capabilities']


def test_effective_capabilities_for_codex_hermes_grok():
    service = ControlService(Mock(), Mock(), Mock())
    all_caps = {'chat', 'stop', 'queue', 'attachments', 'approvals', 'launch', 'history', 'events', 'task_events', 'delete'}

    codex = {
        'id': 'local-codex',
        'kind': 'codex',
        'capabilities': ['chat', 'stop', 'events', 'history', 'approvals', 'queue', 'attachments', 'delete', 'task_events'],
    }
    assert all_caps.issubset(set(service.effective_agent(codex)['capabilities']))

    hermes = {
        'id': 'local-hermes-default',
        'kind': 'hermes',
        'capabilities': ['chat', 'events', 'task_events'],
    }
    service.register_native_command_handler(
        hermes['id'], Mock(), capabilities={'stop', 'attachments', 'history', 'approvals', 'launch', 'delete', 'queue'}
    )
    assert all_caps.issubset(set(service.effective_agent(hermes)['capabilities']))

    grok = {
        'id': 'local-grok',
        'kind': 'grok',
        'capabilities': ['chat', 'stop', 'events', 'history', 'launch', 'delete', 'queue', 'approvals', 'task_events', 'attachments'],
    }
    assert all_caps.issubset(set(service.effective_agent(grok)['capabilities']))
