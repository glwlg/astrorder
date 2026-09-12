"""Store-independent native Hermes session mutation helpers."""
from __future__ import annotations

from collections.abc import Callable, Mapping
from typing import Any

NativeRpc = Callable[[str, dict[str, Any]], Any]


class NativeMutationError(RuntimeError):
    def __init__(self, message: str, *, code: int | None = None) -> None:
        self.code = code
        super().__init__(message)


def rename_native_session(rpc: NativeRpc, session_id: str, title: str) -> None:
    if not isinstance(session_id, str) or not session_id:
        raise ValueError("session_id is required")
    if not isinstance(title, str) or not title.strip():
        raise ValueError("title is required")
    resumed = _call(rpc, "session.resume", {"session_id": session_id, "lazy": True})
    handle = resumed.get("session_id")
    if not isinstance(handle, str) or not handle:
        raise NativeMutationError("native session resume returned no runtime handle")
    _call(rpc, "session.title", {"session_id": handle, "title": title})
    verified = _call(rpc, "session.title", {"session_id": handle})
    if verified.get("title") != title:
        raise NativeMutationError("native session title readback did not match")


def _call(rpc: NativeRpc, method: str, params: dict[str, Any]) -> Mapping[str, Any]:
    response = rpc(method, params)
    error = response.get("error") if isinstance(response, Mapping) else None
    if isinstance(error, Mapping):
        code = error.get("code")
        raise NativeMutationError(
            f"native {method} failed with code {code}",
            code=code if isinstance(code, int) else None,
        )
    result = response.get("result") if isinstance(response, Mapping) else None
    if not isinstance(result, Mapping):
        raise NativeMutationError(f"native {method} returned no confirmation")
    return result


def delete_native_session(rpc: NativeRpc, session_id: str) -> None:
    if not isinstance(session_id, str) or not session_id:
        raise ValueError("session_id is required")
    try:
        deleted = _call(rpc, "session.delete", {"session_id": session_id})
    except NativeMutationError as exc:
        if exc.code == 4007:
            deleted = None
        elif exc.code == 4023:
            active = _call(rpc, "session.active_list", {}).get("sessions")
            if not isinstance(active, list):
                raise NativeMutationError("native active session list is invalid") from exc
            targets = [
                row
                for row in active
                if isinstance(row, Mapping) and row.get("session_key") == session_id
            ]
            if not targets or any(not isinstance(row.get("id"), str) or not row.get("id") for row in targets):
                raise NativeMutationError("native session handle was not found") from exc
            if any(row.get("status") != "idle" for row in targets):
                raise NativeMutationError("native session is running and cannot be deleted") from exc
            for row in targets:
                closed = _call(rpc, "session.close", {"session_id": row["id"]})
                if closed.get("closed") is not True:
                    raise NativeMutationError("native session handle did not close")
            deleted = _call(rpc, "session.delete", {"session_id": session_id})
        else:
            raise
    if deleted is not None and deleted.get("deleted") != session_id:
        raise NativeMutationError("native session delete was not confirmed")
    try:
        _call(
            rpc,
            "session.resume",
            {"session_id": session_id, "lazy": True, "omit_messages": True},
        )
    except NativeMutationError as exc:
        if exc.code == 4007:
            return
        raise
    raise NativeMutationError("native session remains readable after delete")
