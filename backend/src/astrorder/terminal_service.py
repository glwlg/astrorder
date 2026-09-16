import asyncio
import logging
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Optional

logger = logging.getLogger("astrorder.terminal")


def terminal_environment() -> dict[str, str]:
    """Return a real terminal environment without mutating the service process."""
    environment = dict(os.environ)
    environment["TERM"] = "xterm-256color"
    environment["COLORTERM"] = "truecolor"
    return environment


def winpty_environment(environment: dict[str, str]) -> str:
    """Encode a Windows environment block accepted by pywinpty."""
    return "\0".join(
        f"{key}={value}" for key, value in sorted(environment.items(), key=lambda item: item[0].casefold())
    ) + "\0"


def find_shell(is_remote_linux: bool = False) -> str:
    """按平台与要求探测 Shell:
    Windows: 优先 pwsh.exe (PowerShell 7) > git bash > powershell.exe > cmd.exe
    Linux: 优先 zsh > bash > sh
    """
    if sys.platform != "win32" or is_remote_linux:
        for sh in ["zsh", "bash", "sh"]:
            candidate = shutil.which(sh)
            if candidate:
                return candidate
        return "/bin/sh"

    # Windows 平台
    # 1. 优先探测 PowerShell 7 (pwsh.exe)
    pwsh = shutil.which("pwsh")
    if pwsh and os.path.isfile(pwsh):
        return pwsh

    # 尝试查找 WindowsApps 下的真实 pwsh.exe
    local_app_data = os.environ.get("LOCALAPPDATA", "")
    if local_app_data:
        win_apps_pwsh = os.path.join(local_app_data, "Microsoft", "WindowsApps", "pwsh.exe")
        if os.path.isfile(win_apps_pwsh):
            return win_apps_pwsh

    # 2. 其次探测 Git Bash
    for bash_candidate in [
        shutil.which("bash"),
        r"C:\Program Files\Git\bin\bash.exe",
        r"C:\Program Files\Git\usr\bin\bash.exe",
    ]:
        if bash_candidate and os.path.isfile(bash_candidate):
            return bash_candidate

    # 3. 回退系统默认 PowerShell 5.1
    ps_system = os.path.join(os.environ.get("SystemRoot", r"C:\Windows"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
    if os.path.isfile(ps_system):
        return ps_system

    # 4. 兜底 cmd.exe
    return os.environ.get("COMSPEC", "cmd.exe")


class TerminalSession:
    """支持本地 PTY 与 远程 SSH 交互式终端的统一抽象会话"""

    def __init__(
        self,
        workspace: Optional[str] = None,
        ssh_argv: Optional[list[str]] = None,
        remote_workspace: Optional[str] = None,
    ):
        self.workspace = workspace
        self.ssh_argv = ssh_argv
        self.remote_workspace = remote_workspace
        self.pty = None
        self.proc: Optional[subprocess.Popen] = None
        self._is_ssh = bool(ssh_argv)

    async def start(self) -> None:
        environment = terminal_environment()
        if self._is_ssh and self.ssh_argv:
            # 远程 SSH 会话：通过 subprocess.Popen 带有 -tt 伪终端标志启动
            # 兼容跨平台与 Windows 的 OpenSSH 交互
            cmd = list(self.ssh_argv)
            logger.info("启动远程 SSH 终端: %s", cmd)
            self.proc = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                bufsize=0,
                env=environment,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
            # 如果指定了远端工作区，进入后自动 cd 过去
            if self.remote_workspace and self.proc.stdin:
                cd_cmd = f"cd {shlex_quote(self.remote_workspace)} 2>/dev/null || true\n"
                try:
                    self.proc.stdin.write(cd_cmd.encode("utf-8"))
                    self.proc.stdin.flush()
                except Exception:
                    pass
            return

        # 本地终端会话
        shell_path = find_shell()
        logger.info("启动本地终端: %s, 工作区: %s", shell_path, self.workspace)
        working_dir = None
        if self.workspace and os.path.isdir(self.workspace):
            working_dir = self.workspace

        if sys.platform == "win32":
            from winpty import PTY

            self.pty = PTY(120, 30)
            self.pty.spawn(shell_path, cwd=working_dir, env=winpty_environment(environment))
        else:
            # Linux / macOS 使用标准 pty
            import pty

            self.proc = subprocess.Popen(
                [shell_path],
                cwd=working_dir,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                bufsize=0,
                env=environment,
            )

    def read_sync(self) -> str:
        """同步阻塞读取，供后台线程调用"""
        if self._is_ssh and self.proc and self.proc.stdout:
            try:
                data = self.proc.stdout.read(2048)
                if not data:
                    return ""
                return data.decode("utf-8", errors="replace")
            except Exception:
                return ""

        if self.pty:
            try:
                chunk = self.pty.read(blocking=True)
                return chunk
            except Exception:
                return ""
        elif self.proc and self.proc.stdout:
            try:
                data = self.proc.stdout.read(2048)
                if not data:
                    return ""
                return data.decode("utf-8", errors="replace")
            except Exception:
                return ""
        return ""

    def write_sync(self, data: str) -> None:
        """向终端写内容（按键/命令）"""
        if self._is_ssh and self.proc and self.proc.stdin:
            try:
                self.proc.stdin.write(data.encode("utf-8"))
                self.proc.stdin.flush()
            except Exception:
                pass
            return

        if self.pty:
            try:
                self.pty.write(data)
            except Exception:
                pass
        elif self.proc and self.proc.stdin:
            try:
                self.proc.stdin.write(data.encode("utf-8"))
                self.proc.stdin.flush()
            except Exception:
                pass

    def resize(self, cols: int, rows: int) -> None:
        """调整终端视口行列尺寸"""
        if self.pty:
            try:
                self.pty.set_size(cols, rows)
            except Exception:
                pass

    def close(self) -> None:
        if self.pty:
            try:
                self.pty.close()
            except Exception:
                pass
            self.pty = None
        if self.proc:
            try:
                self.proc.terminate()
                self.proc.wait(timeout=1)
            except Exception:
                pass
            self.proc = None


def shlex_quote(s: str) -> str:
    import shlex

    return shlex.quote(s)
