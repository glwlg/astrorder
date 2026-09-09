from __future__ import annotations

from astrorder.native_sessions import discover_native_sessions, history_messages


def test_discovery_keeps_identical_titles_as_distinct_native_sessions_and_projects() -> None:
    pages = {
        0: [
            {"id": "native-a", "title": "same", "cwd": "/work/a"},
            {"id": "native-b", "title": "same", "cwd": "/work/b"},
        ],
        2: [],
    }

    def rpc(method: str, params: dict[str, object], **_: object) -> dict[str, object]:
        if method == "session.list":
            return {"result": {"sessions": pages[int(params["offset"])], "total": 2}}
        if method == "projects.tree":
            return {"result": {
                "projects": [
                    {"id": "project-a", "name": "A", "path": "/work/a", "sessions": [{"id": "native-a"}]},
                    {"id": "project-b", "name": "B", "path": "/work/b", "sessions": [{"id": "native-b"}]},
                ]
            }}
        if method == "projects.project_sessions":
            project_id = str(params["project_id"])
            return {"result": {"project": {
                "id": project_id,
                "name": project_id[-1].upper(),
                "path": f"/work/{project_id[-1]}",
                "sessions": [{"id": f"native-{project_id[-1]}"}],
            }}}
        raise AssertionError(method)

    result = discover_native_sessions(
        rpc,
        source_id="source-one",
        agent_id="runtime-one",
        connection_id=None,
        profile_name="default",
        default_workspace=None,
        page_size=2,
    )

    assert result.complete is True
    assert result.native_count == 2
    assert {row["project_id"] for row in result.sessions} == {"project-a", "project-b"}
    assert {row["id"] for row in result.sessions} == {"native-a", "native-b"}
    assert all(row["id"] == row["source_session_id"] for row in result.sessions)
    assert all(row["control_state"] == "native" for row in result.sessions)
    assert all(row["history_state"] == "available" for row in result.sessions)


def test_native_reasoning_and_tool_details_survive_projection():
    def rpc(*args, **kwargs):
        return {'result': {'messages': [
            {'row_id': 1, 'role': 'assistant', 'text': '答复', 'reasoning_content': '检查数据', 'timestamp': 1788840000},
            {'role': 'tool', 'name': 'execute_code', 'args': {'code': 'print(1)'}, 'context': '执行代码'},
        ]}}
    rows = history_messages(rpc, durable_session_id='native', native_session_id='native', source_id='source', agent_id='agent')
    assert [row['kind'] for row in rows] == ['thinking', 'message', 'tool']
    assert rows[0]['text'] == '检查数据'
    assert rows[1]['text'] == '答复'
    assert rows[2]['tool']['name'] == 'execute_code'
    assert rows[2]['tool']['arguments'] == {'code': 'print(1)'}
    assert rows == history_messages(rpc, durable_session_id='native', native_session_id='native', source_id='source', agent_id='agent')


def test_history_requires_native_row_identity_and_preserves_repeated_text() -> None:
    def rpc(method: str, params: dict[str, object], **_: object) -> dict[str, object]:
        assert method == "session.resume"
        assert params["lazy"] is True
        return {"result": {
            "messages": [
                {"row_id": 10, "role": "user", "text": "repeat"},
                {"row_id": 11, "role": "assistant", "text": "repeat"},
                {"role": "assistant", "text": "no stable id"},
            ]
        }}

    messages = history_messages(
        rpc,
        durable_session_id="history-session",
        native_session_id="native-session",
        source_id="source-one",
        agent_id="runtime-one",
    )

    assert len(messages) == 2
    assert messages[0]["text"] == messages[1]["text"] == "repeat"
    assert messages[0]["id"] != messages[1]["id"]


def test_same_native_session_id_isolated_by_source_identity() -> None:
    def rpc(method: str, params: dict[str, object], **_: object) -> dict[str, object]:
        if method == "session.list":
            return {"result": {"sessions": [{"id": "shared-native-id", "title": "same"}]}}
        return {"result": {"projects": []}}

    left = discover_native_sessions(
        rpc, source_id="source-left", agent_id="runtime-left", connection_id=None,
        profile_name="default", default_workspace=None, page_size=1
    )
    right = discover_native_sessions(
        rpc, source_id="source-right", agent_id="runtime-right", connection_id=None,
        profile_name="default", default_workspace=None, page_size=1
    )

    assert left.sessions[0]["id"] == right.sessions[0]["id"] == "shared-native-id"
    assert left.sessions[0]["agent_id"] != right.sessions[0]["agent_id"]
    assert left.sessions[0]["source_id"] != right.sessions[0]["source_id"]
