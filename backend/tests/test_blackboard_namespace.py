from astrorder.agent_gateway import AgentContext, _resolve_blackboard_ns


class Store:
    scopes = {}

    def list_sessions(self, agent_id):
        assert agent_id == "remote-codex"
        return [
            {"id": "current", "status": "running"},
            {"id": "older", "status": "idle"},
        ]

    def get_session(self, agent_id, session_id):
        return {"agent_id": agent_id, "id": session_id} if agent_id == "remote-codex" else None

    def find_session_by_id(self, session_id):
        return {"agent_id": "remote-codex", "id": session_id}

    def get_session_blackboard_namespace(self, agent_id, session_id):
        return self.scopes.get(f"{agent_id}::{session_id}")


def test_session_default_resolves_to_callers_active_session():
    namespace = _resolve_blackboard_ns(
        {"namespace": "session:default", "caller_agent_id": "remote-codex"},
        AgentContext(store=Store()),
    )
    assert namespace == "session:remote-codex::current"


def test_global_default_and_short_session_id_use_marked_session_scope():
    store = Store()
    store.scopes = {"remote-codex::current": "group:ops"}
    ctx = AgentContext(store=store)

    assert _resolve_blackboard_ns(
        {"namespace": "global", "caller_agent_id": "remote-codex"}, ctx
    ) == "group:ops"
    assert _resolve_blackboard_ns(
        {"namespace": "session:current"}, ctx
    ) == "group:ops"
