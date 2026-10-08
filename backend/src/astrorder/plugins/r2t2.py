from __future__ import annotations

import base64
import io
import json
import os
import socket
import subprocess
import threading
import time
import urllib.request
import wave
import zipfile
from pathlib import Path
from typing import Any, Callable

import httpx

from astrorder.models import WorkspacePreferenceRow

MODEL_NAME = 'Confucius4-R2T2-Q8_0.gguf'
MMPROJ_NAME = 'mmproj-Confucius4-R2T2-Q8_0.gguf'
ENABLED_KEY = 'plugin:r2t2:enabled'
MODEL_BASE = 'https://huggingface.co/netease-youdao/Confucius4-R2T2-GGUF/resolve/main/'
RUNTIME_URL = 'https://github.com/ggml-org/llama.cpp/releases/download/b10950/llama-b10950-bin-win-vulkan-x64.zip'


class LocalR2T2Plugin:
    """Owns the local llama.cpp process used by the R2T2 streaming dictation plugin."""

    def __init__(
        self,
        store: Any,
        *,
        resource_dir: Path | None = None,
        process_factory: Callable[..., Any] = subprocess.Popen,
    ) -> None:
        local = Path(os.environ.get('LOCALAPPDATA', Path.home() / 'AppData' / 'Local'))
        self.resource_dir = Path(resource_dir or os.environ.get('ASTRORDER_R2T2_DIR', local / 'Astrorder' / 'plugins' / 'r2t2'))
        self.runtime_dir = self.resource_dir / 'runtime'
        self.model_dir = self.resource_dir / 'models'
        self.server_path = self.runtime_dir / ('llama-server.exe' if os.name == 'nt' else 'llama-server')
        self.model_path = self.model_dir / MODEL_NAME
        self.mmproj_path = self.model_dir / MMPROJ_NAME
        self.store = store
        self.process_factory = process_factory
        self._http_transport: httpx.BaseTransport | None = None
        self._lock = threading.RLock()
        self._inference_lock = threading.Lock()
        self._cancel = threading.Event()
        self._worker: threading.Thread | None = None
        self._process: Any = None
        self._log_handle: Any = None
        self._port: int | None = None
        self._state = 'disabled'
        self._error: str | None = None
        self._progress: float | None = None
        self._enabled = self._read_enabled()

    def _read_enabled(self) -> bool:
        if self.store is None:
            return False
        try:
            with self.store.session() as db:
                row = db.get(WorkspacePreferenceRow, ENABLED_KEY)
                return bool(row and row.value)
        except Exception:
            return False

    def _persist_enabled(self, enabled: bool) -> None:
        if self.store is None:
            return
        with self.store.session() as db:
            row = db.get(WorkspacePreferenceRow, ENABLED_KEY)
            if row is None:
                db.add(WorkspacePreferenceRow(key=ENABLED_KEY, value=enabled))
            else:
                row.value = enabled
            db.commit()

    def restore(self) -> None:
        if self._enabled:
            self.enable(persist=False)

    def enable(self, *, wait: bool = False, persist: bool = True) -> None:
        with self._lock:
            if persist:
                self._persist_enabled(True)
            self._enabled = True
            self._cancel.clear()
            if self._process is not None and self._process.poll() is None:
                self._state = 'running'
                return
            if self._worker is not None and self._worker.is_alive():
                worker = self._worker
            else:
                self._state = 'starting'
                self._error = None
                worker = threading.Thread(target=self._start_worker, name='astrorder-r2t2', daemon=True)
                self._worker = worker
                worker.start()
        if wait:
            worker.join(timeout=180)
            if worker.is_alive():
                raise RuntimeError('本地语音模型启动超时。')
            with self._lock:
                if self._state != 'running':
                    raise RuntimeError(self._error or '本地语音模型启动失败。')

    def disable(self) -> None:
        self._persist_enabled(False)
        with self._lock:
            self._enabled = False
            self._cancel.set()
            process = self._process
            self._process = None
            self._state = 'stopping' if process is not None else 'disabled'
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        with self._lock:
            self._close_log()
            self._port = None
            self._progress = None
            self._state = 'disabled'
            self._error = None

    def shutdown(self) -> None:
        """Unload the process while keeping the persisted enabled choice for next launch."""
        with self._lock:
            process = self._process
            self._process = None
            self._cancel.set()
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
        with self._lock:
            self._close_log()
            self._port = None
            self._state = 'disabled'

    def _cleanup_orphaned_servers(self) -> None:
        """检查并清理系统中残留的历史 llama-server 孤立进程，避免重复拉起导致显存/内存堆积。"""
        if os.name != 'nt':
            return
        try:
            # 查找所有正在运行的 llama-server.exe
            cmd = ['powershell', '-NoProfile', '-Command', "Get-CimInstance Win32_Process -Filter \"Name = 'llama-server.exe'\" | Select-Object -ExpandProperty ProcessId"]
            res = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
            if res.returncode == 0:
                pids = [int(p.strip()) for p in res.stdout.splitlines() if p.strip().isdigit()]
                current_pid = self._process.pid if self._process is not None else None
                for pid in pids:
                    if pid != current_pid:
                        try:
                            subprocess.run(['taskkill', '/F', '/PID', str(pid)], capture_output=True, timeout=3)
                        except Exception:
                            pass
        except Exception:
            pass

    def _start_worker(self) -> None:
        try:
            self._ensure_resources()
            if self._cancel.is_set() or not self._enabled:
                return
            self._cleanup_orphaned_servers()
            port = self._find_free_port()
            self.resource_dir.mkdir(parents=True, exist_ok=True)
            self._log_handle = (self.resource_dir / 'llama-server.log').open('ab')
            flags = getattr(subprocess, 'CREATE_NO_WINDOW', 0)
            args = [
                str(self.server_path), '-m', str(self.model_path), '--mmproj', str(self.mmproj_path),
                '--device', 'Vulkan0', '--mmproj-device', 'Vulkan0', '-ngl', '99',
                '--host', '127.0.0.1', '--port', str(port), '--parallel', '1', '--ctx-size', '4096',
            ]
            process = self.process_factory(
                args,
                cwd=str(self.runtime_dir),
                stdin=subprocess.DEVNULL,
                stdout=self._log_handle,
                stderr=subprocess.STDOUT,
                creationflags=flags,
            )
            with self._lock:
                self._process = process
                self._port = port
                self._state = 'starting'
            self._wait_until_ready()
            if self._cancel.is_set() or not self._enabled:
                if process.poll() is None:
                    process.terminate()
                return
            with self._lock:
                self._state = 'running'
                self._progress = None
        except Exception as exc:
            with self._lock:
                process = self._process
                self._process = None
                self._port = None
                self._state = 'error' if self._enabled else 'disabled'
                self._error = str(exc)[:500] if self._enabled else None
                self._close_log()
            if process is not None and process.poll() is None:
                process.terminate()

    def _ensure_resources(self) -> None:
        missing = [path for path in (self.server_path, self.model_path, self.mmproj_path) if not path.is_file()]
        if not missing:
            return
        with self._lock:
            self._state = 'installing'
            self._progress = 0.0
        self.runtime_dir.mkdir(parents=True, exist_ok=True)
        self.model_dir.mkdir(parents=True, exist_ok=True)
        downloads: list[tuple[str, Path]] = []
        runtime_zip = self.resource_dir / 'llama-vulkan.zip'
        if not self.server_path.is_file():
            downloads.append((RUNTIME_URL, runtime_zip))
        if not self.model_path.is_file():
            downloads.append((MODEL_BASE + MODEL_NAME, self.model_path))
        if not self.mmproj_path.is_file():
            downloads.append((MODEL_BASE + MMPROJ_NAME, self.mmproj_path))
        total = len(downloads)
        for index, (url, target) in enumerate(downloads):
            self._download(url, target, index, total)
            if self._cancel.is_set():
                raise RuntimeError('安装已取消。')
        if runtime_zip.is_file() and not self.server_path.is_file():
            with zipfile.ZipFile(runtime_zip) as archive:
                archive.extractall(self.runtime_dir)
            runtime_zip.unlink(missing_ok=True)
        if not all(path.is_file() for path in (self.server_path, self.model_path, self.mmproj_path)):
            raise RuntimeError('本地语音插件资源不完整。')

    def _download(self, url: str, target: Path, index: int, total_files: int) -> None:
        partial = target.with_suffix(target.suffix + '.part')
        request = urllib.request.Request(url, headers={'User-Agent': 'Astrorder/0.1'})
        with urllib.request.urlopen(request, timeout=60) as response, partial.open('wb') as output:
            length = int(response.headers.get('Content-Length') or 0)
            received = 0
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                if self._cancel.is_set():
                    raise RuntimeError('安装已取消。')
                output.write(chunk)
                received += len(chunk)
                fraction = received / length if length else 0
                with self._lock:
                    self._progress = (index + fraction) / max(total_files, 1)
        partial.replace(target)

    def _find_free_port(self) -> int:
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            return int(sock.getsockname()[1])

    def _wait_until_ready(self) -> None:
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            if self._cancel.is_set():
                raise RuntimeError('启动已取消。')
            process = self._process
            if process is None or process.poll() is not None:
                raise RuntimeError('llama.cpp 服务在模型加载期间退出。')
            try:
                with httpx.Client(timeout=1) as client:
                    if client.get(f'http://127.0.0.1:{self._port}/health').status_code == 200:
                        return
            except httpx.HTTPError:
                pass
            time.sleep(0.25)
        raise RuntimeError('本地语音模型加载超时。')

    def status(self) -> dict[str, Any]:
        with self._lock:
            if self._state == 'running' and (self._process is None or self._process.poll() is not None):
                self._state = 'error'
                self._error = '本地语音服务意外退出。'
                self._process = None
                self._port = None
            available = all(path.is_file() for path in (self.server_path, self.model_path, self.mmproj_path))
            return {
                'id': 'r2t2-local-voice',
                'name': 'R2T2 本地流式听写',
                'enabled': self._enabled,
                'state': self._state,
                'available': available,
                'progress': self._progress,
                'error': self._error,
                'pid': self._process.pid if self._process is not None and self._process.poll() is None else None,
                'device': 'Vulkan0',
                'model': MODEL_NAME,
            }

    def transcribe_pcm(self, pcm: bytes) -> str:
        with self._lock:
            if self._state != 'running' or self._port is None:
                raise RuntimeError('R2T2 本地流式听写插件未运行。')
            port = self._port
        if not pcm:
            return ''
        buffer = io.BytesIO()
        with wave.open(buffer, 'wb') as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(16000)
            wav.writeframes(pcm)
        encoded = base64.b64encode(buffer.getvalue()).decode('ascii')
        payload = {
            'model': MODEL_NAME,
            'messages': [{'role': 'user', 'content': [
                {'type': 'text', 'text': 'language Chinese'},
                {'type': 'input_audio', 'input_audio': {'data': encoded, 'format': 'wav'}},
            ]}],
            'temperature': 0,
            'max_tokens': 256,
        }
        with self._inference_lock:
            with httpx.Client(timeout=120, transport=self._http_transport) as client:
                response = client.post(f'http://127.0.0.1:{port}/v1/chat/completions', json=payload)
                response.raise_for_status()
                data = response.json()
        try:
            text = data['choices'][0]['message']['content']
        except (KeyError, IndexError, TypeError) as exc:
            raise RuntimeError('本地语音模型返回了无效结果。') from exc
        if not isinstance(text, str):
            raise RuntimeError('本地语音模型返回了无效文本。')
        marker = '<asr_text>'
        if marker in text:
            text = text.split(marker, 1)[1]
        return text.strip()

    def _close_log(self) -> None:
        if self._log_handle is not None:
            self._log_handle.close()
            self._log_handle = None
