from typing import Any
import re

from .connections import ConnectionError


def runtime_rpc(connections, agent_id):
    runtime = connections.get_runtime_by_agent_id(agent_id)
    if runtime is None:
        raise ConnectionError('会话所属运行时未连接。', 503)
    return runtime._rpc if runtime is connections.local else runtime.rpc


def result(rpc, method, params):
    if method == 'session.resume':
        params = {**params, 'omit_messages': True, 'defer_history': True}
    response = rpc(method, params)
    if not isinstance(response, dict) or response.get('error') or not isinstance(response.get('result'), dict):
        code = (response.get('error') or {}).get('code') if isinstance(response, dict) else None
        raise ConnectionError(f'原生接口 {method} 未确认（{code or "无响应"}）。', 502)
    return response['result']


def model_choices(rpc) -> list[dict[str, str]]:
    catalog = result(rpc, 'model.options', {'explicit_only': True, 'include_unconfigured': False})
    choices = {}
    for provider in catalog.get('providers', []):
        if not isinstance(provider, dict) or provider.get('authenticated') is False:
            continue
        slug = provider.get('slug')
        if not isinstance(slug, str) or not slug:
            continue
        for item in provider.get('models', []):
            model = item if isinstance(item, str) else item.get('id') or item.get('name') if isinstance(item, dict) else None
            if not isinstance(model, str) or not model:
                continue
            choices[(slug, model)] = {'provider': slug, 'model': model, 'label': f'{provider.get("name") or slug} · {model}'}
    return list(choices.values())


def open_native_session_ids(rpc) -> list[str]:
    response = rpc('session.active_list', {}, timeout=3)
    if not isinstance(response, dict) or response.get('error') or not isinstance(response.get('result'), dict):
        raise ConnectionError('原生开放会话状态暂不可用。', 503)
    rows = response['result'].get('sessions')
    if not isinstance(rows, list):
        raise ConnectionError('原生开放会话状态格式无效。', 502)
    return list(dict.fromkeys(row['session_key'] for row in rows if isinstance(row, dict) and isinstance(row.get('session_key'), str) and row['session_key']))


def current_session_model(rpc, session_id: str) -> dict[str, Any]:
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    info = resumed.get('info') if isinstance(resumed.get('info'), dict) else {}
    model = info.get('model')
    provider = info.get('provider')
    if not isinstance(model, str) or not model or model in {'unknown', '(unknown)'}:
        handle = resumed.get('session_id')
        if not handle:
            raise ConnectionError('原生会话未返回模型信息。', 502)
        status = result(rpc, 'session.status', {'session_id': handle})
        match = re.search(r'^Model:\s*(.*?)\s+\(([^()]*)\)\s*$', str(status.get('output', '')), re.MULTILINE)
        model, provider = match.groups() if match else (None, None)
    if not isinstance(model, str) or not model or model in {'unknown', '(unknown)'}:
        raise ConnectionError('原生会话未返回模型信息。', 502)
    binding = {'model': model, 'provider': provider if isinstance(provider, str) and provider not in {'', 'unknown', '(unknown)'} else None}
    if isinstance(info.get('branch'), str):
        binding['branch'] = info['branch']
    return binding


def set_session_model(rpc, session_id: str, provider: str, model: str) -> dict[str, Any]:
    if not any(row['provider'] == provider and row['model'] == model for row in model_choices(rpc)):
        raise ConnectionError('所选模型不在原生运行时返回的可用列表中。', 422)
    if any(char.isspace() for char in model + provider):
        raise ConnectionError('模型标识包含无效空白字符。', 422)
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    handle = resumed.get('session_id')
    if not handle:
        raise ConnectionError('原生运行时未返回会话句柄。', 502)
    changed = result(rpc, 'config.set', {'session_id': handle, 'key': 'model', 'value': f'{model} --provider {provider} --session', 'confirm_expensive_model': True})
    if changed.get('confirm_required'):
        raise ConnectionError('原生运行时仍要求确认模型切换；未宣称切换成功。', 409)
    if changed.get('deferred'):
        info = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True}).get('info') or {}
        if info.get('model') != model or info.get('provider') != provider:
            raise ConnectionError('待切换模型尚未通过原生状态读回确认。', 502)
        return {'provider': provider, 'model': model, 'deferred': True}
    status = result(rpc, 'session.status', {'session_id': handle})
    if f'Model: {model} ({provider})' not in str(status.get('output', '')):
        # The agent may still be building, while the native session already has its new override.
        info = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True}).get('info') or {}
        if info.get('model') != model or info.get('provider') != provider:
            raise ConnectionError('模型切换尚未通过原生状态读回确认。', 502)
    return {'provider': provider, 'model': model}


REASONING_EFFORTS = ('none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')


def current_session_reasoning(rpc, session_id: str) -> str | None:
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    handle = resumed.get('session_id')
    if not handle:
        return None
    try:
        data = result(rpc, 'config.get', {'session_id': handle, 'key': 'reasoning'})
    except ConnectionError:
        return None
    effort = data.get('value')
    return effort if effort in REASONING_EFFORTS else None


def set_session_reasoning(rpc, session_id: str, effort: str) -> dict[str, str]:
    if effort not in REASONING_EFFORTS:
        raise ConnectionError('思考强度不在原生支持范围内。', 422)
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    handle = resumed.get('session_id')
    if not handle:
        raise ConnectionError('原生运行时未返回会话句柄。', 502)
    result(rpc, 'config.set', {'session_id': handle, 'key': 'reasoning', 'value': effort})
    confirmed = current_session_reasoning(rpc, session_id)
    if confirmed != effort:
        raise ConnectionError('思考强度尚未通过原生状态读回确认。', 502)
    return {'effort': effort}


HERMES_APPROVAL_MAP = {
    'manual': 'manual',
    'auto': 'smart',
    'full_access': 'off',
}
HERMES_APPROVAL_REVERSE = {
    'manual': 'manual',
    'smart': 'auto',
    'off': 'full_access',
}


def current_session_approval_mode(rpc, session_id: str) -> str:
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    handle = resumed.get('session_id')
    if not handle:
        return 'auto'
    try:
        data = result(rpc, 'config.get', {'session_id': handle, 'key': 'approvals.mode'})
    except ConnectionError:
        return 'auto'
    raw = data.get('value')
    return HERMES_APPROVAL_REVERSE.get(raw, 'auto')


def set_session_approval_mode(rpc, session_id: str, mode: str) -> dict[str, str]:
    if mode not in HERMES_APPROVAL_MAP:
        raise ConnectionError('不支持的审批模式；可选 manual、auto、full_access。', 422)
    native_val = HERMES_APPROVAL_MAP[mode]
    resumed = result(rpc, 'session.resume', {'session_id': session_id, 'lazy': True})
    handle = resumed.get('session_id')
    if not handle:
        raise ConnectionError('原生运行时未返回会话句柄。', 502)
    try:
        result(rpc, 'config.set', {'session_id': handle, 'key': 'approvals.mode', 'value': native_val})
    except ConnectionError as exc:
        raise ConnectionError(f'Hermes 审批模式设置未确认：{exc.detail}', 502) from None
    return {'mode': mode}
