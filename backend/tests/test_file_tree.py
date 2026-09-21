from fastapi.testclient import TestClient
import subprocess
import sys

from astrorder.config import Settings
from astrorder.main import create_app


def test_reveal_file_uses_windows_explorer_selection(tmp_path, monkeypatch):
    target = tmp_path / 'setup.exe'
    target.write_bytes(b'MZ')
    calls = []
    app = create_app(Settings(
        database_url=f'sqlite:///{tmp_path / "reveal.sqlite3"}',
        browser_secret='test-secret',
        attachments_dir=tmp_path / 'attachments',
        static_dir=tmp_path / 'static',
    ))
    monkeypatch.setattr(sys, 'platform', 'win32')
    original_popen = subprocess.Popen
    def popen(args, *positional, **keywords):
        if args[0] == 'explorer.exe':
            calls.append(args)
            return None
        return original_popen(args, *positional, **keywords)
    monkeypatch.setattr('subprocess.Popen', popen)

    with TestClient(app) as client:
        response = client.post('/api/v1/system/open-file?token=test-secret', json={
            'path': str(target), 'action': 'reveal',
        })

    assert response.status_code == 200
    assert calls == [['explorer.exe', '/select,', str(target.resolve())]]


def test_file_tree_reveal_reads_only_the_deep_target_branch(tmp_path):
    root = tmp_path / 'workspace'
    target = root / 'a' / 'b' / 'c' / 'd' / 'README.md'
    target.parent.mkdir(parents=True)
    target.write_text('# deep', encoding='utf-8')
    app = create_app(Settings(
        database_url=f'sqlite:///{tmp_path / "state.sqlite3"}',
        browser_secret='test-secret',
        attachments_dir=tmp_path / 'attachments',
        static_dir=tmp_path / 'static',
    ))

    with TestClient(app) as client:
        response = client.get('/api/v1/files/tree', params={
            'path': str(root), 'depth': 2, 'reveal_path': str(target), 'token': 'test-secret',
        })
    assert response.status_code == 200

    def contains(nodes):
        return any(node['path'] == str(target) or contains(node.get('children', [])) for node in nodes)

    assert contains(response.json()['items'])
