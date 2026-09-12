import pytest

from astrorder.codex_policy import codex_turn_policy


@pytest.mark.parametrize(
    ("mode", "expected"),
    [
        (
            "auto",
            {
                "approvalPolicy": "on-request",
                "sandboxPolicy": {"type": "workspaceWrite"},
                "approvalsReviewer": "auto_review",
            },
        ),
        ("manual", {"approvalPolicy": "untrusted"}),
        (
            "full_access",
            {
                "approvalPolicy": "never",
                "sandboxPolicy": {"type": "dangerFullAccess"},
            },
        ),
    ],
)
def test_codex_turn_policy_matches_the_declared_approval_mode(mode, expected):
    assert codex_turn_policy(mode) == expected


def test_codex_turn_policy_rejects_unknown_mode():
    with pytest.raises(ValueError, match="approval mode"):
        codex_turn_policy("not-a-mode")
