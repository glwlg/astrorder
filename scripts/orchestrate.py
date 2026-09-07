"""Run two bounded Hermes development sessions, then a separate integration session.

Agent exit/report gates permit handoff, not a claim of verified completion.
No shell invocation, no global config changes, no access to user credentials.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import UTC, datetime
from pathlib import Path

ROLES = {
    'frontend': ('gpt-5.6-luna', 'Astrorder 前端开发'),
    'backend': ('gpt-5.6-luna', 'Astrorder 后端开发'),
    'integration': ('gpt-5.6-terra', 'Astrorder 全栈联调'),
}


def build_command(executable: str, root: Path, role: str, run_id: str,
                  session_id: str | None = None) -> list[str]:
    model, title = ROLES[role]
    session_args = ['--resume', session_id] if session_id else [
        '--continue', f'{title} {run_id}', '--create-if-missing',
    ]
    return [
        executable, 'chat', '--provider', 'ocx', '--model', model,
        '--reasoning', 'max', *session_args,
        '--oneshot', '--no-restore-cwd', '--in', str(root), '--pass-session-id',
        '--max-turns', '240', '--run-budget', '7200',
        '--query-file', str(root / 'docs' / 'tasks' / f'{role}.md'),
    ]


def worker_environment(root: Path, inherited: dict[str, str]) -> dict[str, str]:
    if not root.is_absolute() or not root.is_dir():
        raise ValueError('Worker root must be an existing absolute directory')
    # Same per-worker anchor used by Hermes kanban dispatch; cwd alone is insufficient.
    return dict(inherited, TERMINAL_CWD=str(root), PYTHONIOENCODING='utf-8',
                PYTHONUNBUFFERED='1', NO_COLOR='1')


def ready_for_integration(root: Path, codes: dict[str, int]) -> bool:
    return all(
        codes.get(role) == 0
        and (root / 'docs' / 'reports' / f'{role}.md').is_file()
        and (root / 'docs' / 'reports' / f'{role}.md').stat().st_size > 0
        for role in ('frontend', 'backend')
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--resume-frontend')
    parser.add_argument('--resume-backend')
    args = parser.parse_args()
    resume_ids = {'frontend': args.resume_frontend, 'backend': args.resume_backend}
    root = Path(__file__).resolve().parents[1]
    runtime = root / '.runtime'
    runtime.mkdir(exist_ok=True)
    executable = shutil.which('hermes')
    if not executable:
        raise RuntimeError('Hermes executable not found')
    # Exclusive lock prevents duplicate development writers or overlapping resumes.
    lock_path = runtime / 'orchestration.lock'
    lock_fd = os.open(lock_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY)
    os.write(lock_fd, str(os.getpid()).encode('ascii'))
    os.close(lock_fd)
    run_id = datetime.now(UTC).strftime('%Y%m%dT%H%M%SZ')
    state = {'run_id': run_id, 'stage': 'development', 'jobs': {}, 'verified_complete': False}
    mutex = threading.Lock()
    state_path = runtime / 'orchestration.json'
    processes: list[subprocess.Popen] = []

    def persist():
        temporary = state_path.with_suffix('.tmp')
        temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding='utf-8')
        temporary.replace(state_path)

    def run_role(role: str) -> int:
        session_id = resume_ids.get(role)
        command = build_command(executable, root, role, run_id, session_id)
        task_path = root / 'docs' / 'tasks' / f'{role}.md'
        query_path = runtime / f'{role}-{run_id}-query.md'
        query_path.write_text(
            f'Execution root (already initialized, do not recreate): {root.as_posix()}\n'
            f'First verify terminal pwd and read {root.as_posix()}/docs/PRODUCT.md and '
            f'{root.as_posix()}/docs/CONTRACT.md using ABSOLUTE paths. '
            'Do not search the home directory or whole machine for this project. '
            'All relative task paths below are relative to this root. '
            'Use explicit absolute workdir/path on every tool call if the inherited tool state differs. '
            'Previous attempt was paused with user permission to correct tool cwd. '
            'Preserve existing code and resume from actual files/tests, do not reset or scaffold again.\n\n'
            + task_path.read_text(encoding='utf-8'), encoding='utf-8',
        )
        command[command.index('--query-file') + 1] = str(query_path)
        log_path = runtime / f'{role}-{run_id}.log'
        env = worker_environment(root, dict(os.environ))
        with log_path.open('w', encoding='utf-8') as log:
            process = subprocess.Popen(command, cwd=root, stdout=log, stderr=subprocess.STDOUT, env=env)
            with mutex:
                processes.append(process)
                state['jobs'][role] = {
                    'pid': process.pid, 'model': ROLES[role][0], 'provider': 'ocx',
                    'reasoning': 'max', 'title': ROLES[role][1],
                    'resume_session_id': session_id, 'terminal_cwd': env['TERMINAL_CWD'],
                    'status': 'running', 'log': str(log_path), 'command': command,
                }
                persist()
            print(f'START {role}: pid={process.pid}, model={ROLES[role][0]}, reasoning=max', flush=True)
            try:
                code = process.wait(timeout=7800)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                code = 124
        # Store only session identifiers from logs, not their contents.
        content = log_path.read_text(encoding='utf-8', errors='replace')
        identifiers = list(dict.fromkeys(re.findall(r'\b\d{8}_\d{6}_[a-zA-Z0-9]+\b', content)))
        with mutex:
            state['jobs'][role].update(status='exited', exit_code=code, session_ids=identifiers)
            persist()
        print(f'EXIT {role}: code={code}; report={(root / "docs/reports" / (role + ".md")).exists()}', flush=True)
        return code

    try:
        persist()
        codes = {}
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = {pool.submit(run_role, role): role for role in ('frontend', 'backend')}
            for future in as_completed(futures):
                codes[futures[future]] = future.result()
        if not ready_for_integration(root, codes):
            state['stage'] = 'blocked_before_integration'
            state['reason'] = 'A development process failed or a handoff report is absent; no integration session launched.'
            persist()
            print(state['reason'], flush=True)
            return 2
        state['stage'] = 'integration'
        persist()
        print('Both development writers exited with reports; starting independent Terra integration. This is NOT test acceptance.', flush=True)
        code = run_role('integration')
        state['stage'] = 'integration_exited_needs_review' if code == 0 else 'integration_failed'
        persist()
        print(f'Integration exit={code}; inspect docs/reports/integration.md and independently verify.', flush=True)
        return code
    finally:
        # Only terminate processes this orchestrator created, never a live Agent service.
        for process in processes:
            if process.poll() is None:
                process.terminate()
        lock_path.unlink(missing_ok=True)


if __name__ == '__main__':
    sys.exit(main())
