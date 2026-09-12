import pytest

from astrorder.native_session_mutation import (
    NativeMutationError,
    delete_native_session,
    rename_native_session,
)


def test_delete_native_session_closes_only_exact_idle_handle_and_verifies_absence():
    calls: list[tuple[str, dict[str, object]]] = []
    deletes = iter([
        {"error": {"code": 4023}},
        {"result": {"deleted": "native-session"}},
    ])

    def rpc(method, params):
        calls.append((method, dict(params)))
        if method == "session.delete":
            return next(deletes)
        if method == "session.active_list":
            return {"result": {"sessions": [{"id": "handle-1", "session_key": "native-session", "status": "idle"}]}}
        if method == "session.close":
            return {"result": {"closed": True}}
        if method == "session.list":
            return {"result": {"sessions": [], "has_more": False}}
        if method == "session.resume":
            return {"error": {"code": 4007}}
        raise AssertionError(method)

    delete_native_session(rpc, "native-session")

    assert ("session.close", {"session_id": "handle-1"}) in calls
    assert calls.count(("session.delete", {"session_id": "native-session"})) == 2


def test_delete_native_session_never_closes_running_handle():
    def rpc(method, params):
        del params
        if method == "session.delete":
            return {"error": {"code": 4023}}
        if method == "session.active_list":
            return {"result": {"sessions": [{"id": "handle-1", "session_key": "native-session", "status": "running"}]}}
        raise AssertionError(method)

    try:
        delete_native_session(rpc, "native-session")
    except RuntimeError as exc:
        assert "running" in str(exc)
    else:
        raise AssertionError("running native handle must block deletion")


def test_rename_native_session_uses_runtime_handle_and_requires_readback():
    calls = []

    def rpc(method, params):
        calls.append((method, params))
        if method == "session.resume":
            return {"result": {"session_id": "runtime-handle"}}
        if method == "session.title" and "title" in params:
            return {"result": {"updated": True}}
        if method == "session.title":
            return {"result": {"title": "Renamed"}}
        raise AssertionError(method)

    rename_native_session(rpc, "durable-session", "Renamed")

    assert calls == [
        ("session.resume", {"session_id": "durable-session", "lazy": True}),
        ("session.title", {"session_id": "runtime-handle", "title": "Renamed"}),
        ("session.title", {"session_id": "runtime-handle"}),
    ]


def test_rename_native_session_rejects_mismatched_readback():
    def rpc(method, params):
        if method == "session.resume":
            return {"result": {"session_id": "runtime-handle"}}
        if method == "session.title" and "title" in params:
            return {"result": {"updated": True}}
        return {"result": {"title": "Old title"}}

    with pytest.raises(NativeMutationError, match="readback"):
        rename_native_session(rpc, "durable-session", "Renamed")
