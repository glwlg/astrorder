from __future__ import annotations

import hashlib
import json
import subprocess
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

try:
    from astrorder.connections import run_subprocess_hidden
except ImportError:  # Standalone snapshot script executed over SSH.
    run_subprocess_hidden = subprocess.run


@dataclass(frozen=True)
class WorkspaceSnapshot:
    workspace: Path
    repo_root: Path
    branch: str
    head: str
    change_fingerprint: str


def _git(workspace: Path, *args: str) -> str:
    result = run_subprocess_hidden(
        ["git", "-C", str(workspace), *args],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    if result.returncode != 0:
        raise ValueError(result.stderr.strip() or f"git {' '.join(args)} failed")
    return result.stdout.strip()


def capture_workspace_snapshot(workspace: str | Path) -> WorkspaceSnapshot:
    path = Path(workspace).expanduser().resolve()
    if not path.is_dir():
        raise ValueError(f"工作区不存在：{path}")
    repo_root = Path(_git(path, "rev-parse", "--show-toplevel")).resolve()
    branch = _git(path, "symbolic-ref", "--short", "-q", "HEAD") or _git(path, "rev-parse", "--short", "HEAD")
    head = _git(path, "rev-parse", "HEAD")
    status = run_subprocess_hidden(
        ["git", "-C", str(path), "status", "--porcelain=v1", "-z", "--untracked-files=all"],
        capture_output=True,
        check=False,
    )
    if status.returncode != 0:
        raise ValueError(status.stderr.decode("utf-8", "replace").strip() or "无法读取 Git 工作区状态")
    digest = hashlib.sha256(status.stdout)
    for args in (("diff", "--binary", "HEAD"), ("ls-files", "--others", "--exclude-standard", "-z")):
        result = run_subprocess_hidden(["git", "-C", str(path), *args], capture_output=True, check=False)
        if result.returncode != 0:
            raise ValueError(result.stderr.decode("utf-8", "replace").strip() or "无法读取 Git 工作区变更")
        digest.update(result.stdout)
        if args[0] == "ls-files":
            for name in result.stdout.split(b"\0"):
                if name:
                    digest.update(hashlib.sha256((repo_root / name.decode("utf-8", "surrogateescape")).read_bytes()).digest())
    return WorkspaceSnapshot(path, repo_root, branch, head, digest.hexdigest())


def capture_remote_workspace_snapshot(workspace: str, settings: dict, connection_id: str) -> WorkspaceSnapshot:
    """Run the same snapshot code in the SSH environment, never resolving remote paths locally."""
    from astrorder.ssh_transport import SshNativeRuntime, build_remote_python_command

    runtime = SshNativeRuntime(settings, connection_id, 0, None, None, connector_secret=None)
    argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
    script = Path(__file__).read_text(encoding="utf-8") + (
        "\nimport json\n"
        f"s = capture_workspace_snapshot({json.dumps(workspace)})\n"
        "print(json.dumps({'workspace': str(s.workspace), 'repo_root': str(s.repo_root), "
        "'branch': s.branch, 'head': s.head, 'change_fingerprint': s.change_fingerprint}))\n"
    )
    try:
        result = run_subprocess_hidden(
            argv + [build_remote_python_command(script)], capture_output=True, text=True,
            encoding="utf-8", errors="replace", timeout=30, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise ValueError("无法连接远程工作区以验证 Git 快照") from exc
    if result.returncode:
        raise ValueError("远程工作区快照读取失败，请检查 SSH 连接和远程路径")
    try:
        data = json.loads(result.stdout.strip())
        return WorkspaceSnapshot(PurePosixPath(data["workspace"]), PurePosixPath(data["repo_root"]), data["branch"], data["head"], data["change_fingerprint"])
    except (ValueError, KeyError, TypeError) as exc:
        raise ValueError("远程工作区快照响应无效") from exc


def snapshots_match(left: WorkspaceSnapshot, right: WorkspaceSnapshot) -> bool:
    return (
        left.repo_root == right.repo_root
        and left.branch == right.branch
        and left.head == right.head
        and left.change_fingerprint == right.change_fingerprint
    )
