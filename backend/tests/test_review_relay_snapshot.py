from pathlib import Path
import subprocess

from astrorder.core.review_relay import capture_workspace_snapshot, snapshots_match


def test_workspace_snapshot_captures_repo_identity_and_changes(tmp_path: Path) -> None:
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "config", "user.email", "test@example.com"], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "config", "user.name", "Test"], check=True)
    (tmp_path / "file.txt").write_text("one", encoding="utf-8")
    subprocess.run(["git", "-C", str(tmp_path), "add", "file.txt"], check=True)
    subprocess.run(["git", "-C", str(tmp_path), "commit", "-qm", "initial"], check=True)

    clean = capture_workspace_snapshot(str(tmp_path))
    assert clean.repo_root == tmp_path.resolve()
    assert clean.branch
    assert len(clean.head) == 40
    assert clean.change_fingerprint

    (tmp_path / "file.txt").write_text("two", encoding="utf-8")
    changed = capture_workspace_snapshot(str(tmp_path))
    assert not snapshots_match(clean, changed)
    assert clean.repo_root == changed.repo_root
    assert clean.branch == changed.branch
    assert clean.head == changed.head
