"""Shared, documented Codex turn policy mapping."""
from __future__ import annotations

from typing import Any


APPROVAL_MODES = frozenset({"manual", "auto", "full_access"})


def codex_turn_policy(mode: str) -> dict[str, Any]:
    """Return the exact native turn policy for an Astrorder approval mode."""
    if mode == "auto":
        return {
            "approvalPolicy": "on-request",
            "sandboxPolicy": {"type": "workspaceWrite"},
            "approvalsReviewer": "auto_review",
        }
    if mode == "manual":
        return {"approvalPolicy": "untrusted"}
    if mode == "full_access":
        return {
            "approvalPolicy": "never",
            "sandboxPolicy": {"type": "dangerFullAccess"},
        }
    raise ValueError("approval mode is unsupported")
