"""Astrorder server package."""
import importlib
from typing import Any

# 动态属性解析：保持顶级包极简，仅在外部显式访问旧符号时按需透明解析到子包
_SUBMODULE_MAP = {
    # agents
    "agent_gateway": "astrorder.agents.gateway",
    "agent_mcp": "astrorder.agents.mcp",
    "agent_cli": "astrorder.agents.cli",
    "agent_registry": "astrorder.agents.registry",
    # native
    "native_codex": "astrorder.native.codex",
    "native_sessions": "astrorder.native.sessions",
    "native_commands": "astrorder.native.commands",
    "native_controls": "astrorder.native.controls",
    "native_attachments": "astrorder.native.attachments",
    "native_history_page": "astrorder.native.history_page",
    "native_observers": "astrorder.native.observers",
    "native_session_mutation": "astrorder.native.session_mutation",
    "native_user_activity": "astrorder.native.user_activity",
    "native_identity_migration": "astrorder.native.identity_migration",
    # jev
    "jev_browser": "astrorder.jev.browser",
    "jev_client": "astrorder.jev.client",
    # observers
    "observer_io": "astrorder.observers.io",
    "observer_plugin": "astrorder.observers.plugin",
    # adapters
    "codex_inputs": "astrorder.adapters.codex.inputs",
    "codex_policy": "astrorder.adapters.codex.policy",
    "codex_tasks": "astrorder.adapters.codex.tasks",
    "hermes_approvals": "astrorder.adapters.hermes.approvals",
    "hermes_compaction": "astrorder.adapters.hermes.compaction",
    "hermes_inputs": "astrorder.adapters.hermes.inputs",
    # core
    "analytics": "astrorder.core.analytics",
    "attachments": "astrorder.core.attachments",
    "auth": "astrorder.core.auth",
    "background_tasks": "astrorder.core.background_tasks",
    "blackboard_catalog": "astrorder.core.blackboard_catalog",
    "bot_groups": "astrorder.core.bot_groups",
    "environment_connections": "astrorder.core.environment_connections",
    "events": "astrorder.core.events",
    "gateway_config": "astrorder.core.gateway_config",
    "handoff": "astrorder.core.handoff",
    "llm_config": "astrorder.core.llm_config",
    "model_sync": "astrorder.core.model_sync",
    "model_sync_service": "astrorder.core.model_sync_service",
    "staging_files": "astrorder.core.staging_files",
    "swarm_service": "astrorder.core.swarm_service",
    "system_environment": "astrorder.core.system_environment",
    "terminal_service": "astrorder.core.terminal_service",
    "timeutil": "astrorder.core.timeutil",
    "workspace_preferences": "astrorder.core.workspace_preferences",
}

def __getattr__(name: str) -> Any:
    if name in _SUBMODULE_MAP:
        return importlib.import_module(_SUBMODULE_MAP[name])
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
