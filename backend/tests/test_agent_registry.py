from unittest.mock import Mock
from astrorder.agents.registry import current_agents

def test_retired_empty_agent_is_not_another_disconnected_connection():
    store=Mock();store.list_ssh_connections.return_value=[{'id':'current'}]
    store.list_agents.return_value=[{'id':'old','connection_id':'removed','status':'disconnected'},{'id':'offline','connection_id':'current','status':'disconnected'},{'id':'retained','connection_id':'removed','status':'disconnected'},{'id':'live','connection_id':'removed','status':'ready'}]
    store.list_sessions.return_value=[{'agent_id':'retained'}]
    assert [a['id'] for a in current_agents(store)]==['offline','retained','live']
    assert len(store.list_agents())==4
