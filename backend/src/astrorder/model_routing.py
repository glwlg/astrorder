"""Deterministic pre-dispatch model tier routing without prompt-content inspection."""
from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

from .config import Settings
from .store import Store
from .timeutil import utc_now


@dataclass(frozen=True)
class ModelRouteTarget:
    provider: str
    model: str
    effort: str | None = None

    def __post_init__(self) -> None:
        if not self.provider or not self.model:
            raise ValueError("model route target requires provider and model")


@dataclass(frozen=True)
class ModelRouteDecision:
    tier: str
    target: ModelRouteTarget
    reason: str
    cost_units: int


@dataclass(frozen=True)
class ModelRoutingPolicy:
    small: ModelRouteTarget
    large: ModelRouteTarget
    daily_budget_units: int
    small_cost_units: int = 1
    large_cost_units: int = 5
    large_text_threshold: int = 2000

    def __post_init__(self) -> None:
        if self.daily_budget_units < 1:
            raise ValueError("daily model routing budget must be positive")
        if self.small_cost_units < 1 or self.large_cost_units < self.small_cost_units:
            raise ValueError("model routing cost units are invalid")
        if self.large_text_threshold < 1:
            raise ValueError("large text threshold must be positive")

    def decide(
        self,
        command: dict[str, Any],
        *,
        spent_units: int,
        previous_state: str | None,
    ) -> ModelRouteDecision | None:
        if command.get("action") != "send":
            return None
        if spent_units < 0:
            raise ValueError("spent model routing units cannot be negative")
        attachments = command.get("attachments") or []
        text = command.get("text") if isinstance(command.get("text"), str) else ""
        if attachments:
            desired, reason = "large", "attachment"
        elif len(text) >= self.large_text_threshold:
            desired, reason = "large", "long_input"
        elif previous_state in {"failed", "unknown"}:
            desired, reason = "large", "previous_unconfirmed"
        else:
            desired, reason = "small", "simple"
        if (
            desired == "large"
            and spent_units + self.large_cost_units > self.daily_budget_units
        ):
            return ModelRouteDecision(
                tier="small",
                target=self.small,
                reason="budget_guard",
                cost_units=self.small_cost_units,
            )
        if desired == "large":
            return ModelRouteDecision(
                tier="large",
                target=self.large,
                reason=reason,
                cost_units=self.large_cost_units,
            )
        return ModelRouteDecision(
            tier="small",
            target=self.small,
            reason=reason,
            cost_units=self.small_cost_units,
        )


ModelRouteApplier = Callable[[str, str, ModelRouteTarget], Awaitable[None]]


def _target(value: str, effort: str) -> ModelRouteTarget:
    provider, model = value.split("/", 1)
    return ModelRouteTarget(provider, model, effort)


class ModelRouter:
    """Serialize model selection, native confirmation, and persistent unit charging."""

    def __init__(
        self,
        store: Store,
        settings: Settings,
        apply: ModelRouteApplier,
        *,
        retry_delay: float = 1.0,
    ) -> None:
        if retry_delay < 0:
            raise ValueError("model route retry delay cannot be negative")
        self._store = store
        self._settings = settings
        self._apply = apply
        self._retry_delay = retry_delay
        self._lock = asyncio.Lock()

    def _policy(self, agent_kind: str) -> ModelRoutingPolicy:
        if agent_kind == "hermes":
            small, large = self._settings.hermes_small_model, self._settings.hermes_large_model
        elif agent_kind == "codex":
            small, large = self._settings.codex_small_model, self._settings.codex_large_model
        else:
            raise ValueError("model routing agent kind is unsupported")
        if not isinstance(small, str) or not isinstance(large, str):
            raise TypeError("model routing targets are unavailable")
        return ModelRoutingPolicy(
            small=_target(small, self._settings.model_routing_small_effort),
            large=_target(large, self._settings.model_routing_large_effort),
            daily_budget_units=self._settings.model_routing_daily_budget_units,
            small_cost_units=self._settings.model_routing_small_cost_units,
            large_cost_units=self._settings.model_routing_large_cost_units,
            large_text_threshold=self._settings.model_routing_large_text_threshold,
        )

    async def route(
        self, command: dict[str, Any], *, agent_kind: str
    ) -> dict[str, Any] | None:
        if not self._settings.model_routing_enabled or command.get("action") != "send":
            return None
        async with self._lock:
            existing = self._store.latest_model_route(
                command["agent_id"], command["session_id"]
            )
            if existing is not None and existing["command_id"] == command["id"]:
                return existing
            commands = self._store.list_commands(command["agent_id"], command["session_id"])
            previous_state = next(
                (
                    row["state"]
                    for row in reversed(commands)
                    if row["id"] != command["id"] and row["action"] == "send"
                ),
                None,
            )
            now = utc_now()
            day_start = now.replace(hour=0, minute=0, second=0, microsecond=0)
            spent = self._store.model_routing_units(command["agent_id"], day_start)
            decision = self._policy(agent_kind).decide(
                command,
                spent_units=spent,
                previous_state=previous_state,
            )
            if decision is None:
                return None
            try:
                await self._apply(
                    command["agent_id"], command["session_id"], decision.target
                )
            except Exception:  # noqa: BLE001 - one idempotent control retry, never command retry
                if self._retry_delay:
                    await asyncio.sleep(self._retry_delay)
                await self._apply(
                    command["agent_id"], command["session_id"], decision.target
                )
            route = {
                "agent_id": command["agent_id"],
                "session_id": command["session_id"],
                "command_id": command["id"],
                "tier": decision.tier,
                "provider": decision.target.provider,
                "model": decision.target.model,
                "effort": decision.target.effort,
                "reason": decision.reason,
                "cost_units": decision.cost_units,
            }
            stored, _created = self._store.record_model_route(route, created_at=now)
            return stored
