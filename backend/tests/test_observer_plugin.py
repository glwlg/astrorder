import json
from unittest.mock import Mock

import pytest

from astrorder import observer_plugin as plugin

def test_migrate_only_owned_hooks_after_native_plugin_verification(tmp_path,monkeypatch):
    original={'hooks':{'Stop':[{'hooks':[{'type':'command','command':'other','statusMessage':'Other'}, {'type':'command','command':'python astrorder-observer/observer.py','statusMessage':plugin.MARKER}]}]}}
    (tmp_path/'hooks.json').write_text(json.dumps(original))
    (tmp_path/'config.toml').write_text('[mcp_servers.astrorder.http_headers]\nAuthorization = "Bearer token"\n')
    monkeypatch.setattr(plugin.subprocess,'run',Mock(return_value=Mock(returncode=0)))
    monkeypatch.setattr(plugin,'verify_plugin',Mock(return_value={'plugin_hooks':10,'source':'plugin'}))
    result=plugin.install_plugin(tmp_path,'python','print("test")','codex')
    assert result['removed']==1
    assert json.loads((tmp_path/'hooks.json').read_text())['hooks']['Stop'][0]['hooks']==[original['hooks']['Stop'][0]['hooks'][0]]
    manifest=json.loads((tmp_path/'astrorder-observer/marketplace/plugins/astrorder/.codex-plugin/plugin.json').read_text(encoding='utf-8'))
    assert manifest['interface']['displayName']=='Astrorder · 星序'
    hooks=json.loads((tmp_path/'astrorder-observer/marketplace/plugins/astrorder/hooks/hooks.json').read_text(encoding='utf-8'))['hooks']
    assert hooks['PermissionRequest'][0]['hooks'][0]['timeout']==600 and hooks['Stop'][0]['hooks'][0]['timeout']==3
    assert 'X-Astrorder-Agent-Id = "local-codex"' in (tmp_path/'config.toml').read_text()
    assert plugin.install_plugin(tmp_path,'python','print("test")','codex')['removed']==0

def test_failed_plugin_verification_preserves_legacy(tmp_path,monkeypatch):
    raw='{"hooks":{"Stop":[]}}';(tmp_path/'hooks.json').write_text(raw)
    monkeypatch.setattr(plugin.subprocess,'run',Mock(return_value=Mock(returncode=0)))
    monkeypatch.setattr(plugin,'verify_plugin',Mock(side_effect=RuntimeError('not detected')))
    with pytest.raises(RuntimeError): plugin.install_plugin(tmp_path,'python','pass','codex')
    assert (tmp_path/'hooks.json').read_text()==raw
