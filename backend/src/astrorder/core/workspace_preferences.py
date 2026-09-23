"""Shared preferences for the single authenticated Astrorder workspace."""
from typing import Annotated, Literal

from fastapi import APIRouter, Depends, Request, Response
from pydantic import BaseModel, ConfigDict, Field, StrictBool, StringConstraints
from sqlalchemy import select
from sqlalchemy.dialects.sqlite import insert

from .auth import require_browser
from astrorder.models import WorkspacePreferenceRow

Key = Annotated[str, StringConstraints(min_length=1, max_length=1024)]


class Appearance(BaseModel):
    model_config = ConfigDict(extra="forbid")
    color: Literal['', 'red', 'orange', 'amber', 'yellow', 'lime', 'green', 'emerald', 'teal', 'cyan', 'sky', 'blue', 'indigo', 'violet', 'purple', 'fuchsia', 'pink', 'rose', 'slate'] = ''
    icon: Literal['folder', 'rocket', 'tools', 'database', 'cloud', 'code', 'package', 'book', 'bug', 'flame', 'zap', 'star', 'heart', 'globe', 'server', 'terminal', 'lightbulb', 'shield', 'target', 'gear', 'coffee'] = 'folder'


class PreferencePatch(BaseModel):
    model_config = ConfigDict(extra="forbid")
    appearance: dict[Key, Appearance | None] = Field(default_factory=dict, max_length=10000)
    session_pins: dict[Key, StrictBool] = Field(default_factory=dict, max_length=10000)
    pinned_projects: list[Key] | None = Field(default=None, max_length=10000)
    project_order: list[Key] | None = Field(default=None, max_length=10000)


def authenticated(request: Request, response: Response) -> None:
    require_browser(request, request.app.state.settings)
    response.headers["Cache-Control"] = "no-store"


router = APIRouter(prefix="/api/v1/preferences", dependencies=[Depends(authenticated)])


def read_preferences(db) -> dict:
    result = {"appearance": {}, "session_pins": {}, "pinned_projects": [], "project_order": []}
    for row in db.scalars(select(WorkspacePreferenceRow)):
        category, _, key = row.key.partition(":")
        if category in ("appearance", "session_pins"):
            if row.value is not None:
                result[category][key] = row.value
        elif category in ("pinned_projects", "project_order"):
            result[category] = row.value
    return result


def write_preferences(request: Request, payload: PreferencePatch, *, importing: bool = False) -> dict:
    values = {}
    for category in ("appearance", "session_pins"):
        for key, value in getattr(payload, category).items():
            values[f"{category}:{key}"] = value.model_dump() if isinstance(value, Appearance) else value
    for category in ("pinned_projects", "project_order"):
        value = getattr(payload, category)
        if value is not None and (value or not importing):
            values[category] = list(dict.fromkeys(value))
    with request.app.state.store.session() as db:
        for key, value in values.items():
            statement = insert(WorkspacePreferenceRow).values(key=key, value=value)
            # Tombstones remain stored, so importing an old browser cannot undo removals.
            statement = statement.on_conflict_do_nothing(index_elements=["key"]) if importing else statement.on_conflict_do_update(index_elements=["key"], set_={"value": value})
            db.execute(statement)
        return read_preferences(db)


@router.get("")
def get_preferences(request: Request):
    with request.app.state.store.session() as db:
        return read_preferences(db)


@router.patch("")
def patch_preferences(payload: PreferencePatch, request: Request):
    return write_preferences(request, payload)


@router.post("/import")
def import_preferences(payload: PreferencePatch, request: Request):
    return write_preferences(request, payload, importing=True)
