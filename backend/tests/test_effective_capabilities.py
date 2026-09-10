from unittest.mock import Mock

from astrorder.service import ControlService


def test_effective_capabilities_include_live_native_channels_without_mutating_plugin():
    service=ControlService(Mock(),Mock(),Mock())
    agent={'id':'local-hermes-default','kind':'hermes','capabilities':['chat','events','task_events'],'limitation':'old plugin text'}
    service._native_command_handlers[agent['id']]=Mock()
    service._native_history_handlers[agent['id']]=Mock()
    effective=service.effective_agent(agent)
    assert {'chat','history','stop','task_events','attachments'}.issubset(effective['capabilities'])
    assert 'approvals' not in effective['capabilities']
    assert agent['capabilities']==['chat','events','task_events']
    assert '当前运行时' in effective['limitation']
    assert '图片' in effective['limitation']
    service._native_command_handlers.clear(); service._native_history_handlers.clear()
    assert 'stop' not in service.effective_agent(agent)['capabilities']
