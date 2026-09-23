"""Load child-process environment from the host's current system settings."""
from __future__ import annotations

import os
import re
from collections.abc import Mapping

_WINDOWS_MACHINE_ENVIRONMENT = r"SYSTEM\CurrentControlSet\Control\Session Manager\Environment"
_WINDOWS_USER_ENVIRONMENT = r"Environment"
_PERCENT_VARIABLE = re.compile(r"%([^%]+)%")


def _valid_entry(name: object, value: object) -> bool:
    return (
        isinstance(name, str)
        and bool(name)
        and "=" not in name
        and "\x00" not in name
        and isinstance(value, (str, int, float))
    )


def _overlay(target: dict[str, str], values: Mapping[str, object], *, case_insensitive: bool) -> None:
    names = {key.casefold() if case_insensitive else key: key for key in target}
    for name, value in values.items():
        if not _valid_entry(name, value):
            continue
        key = name.casefold() if case_insensitive else name
        previous = names.get(key)
        if previous and previous != name:
            del target[previous]
        target[name] = str(value)
        names[key] = name


def merge_system_environment(
    *,
    process_environment: Mapping[str, object],
    machine_environment: Mapping[str, object] | None = None,
    user_environment: Mapping[str, object] | None = None,
    case_insensitive: bool | None = None,
) -> dict[str, str]:
    """Merge machine, user, and inherited process settings without naming any token."""
    case_insensitive = os.name == "nt" if case_insensitive is None else case_insensitive
    environment: dict[str, str] = {}
    _overlay(environment, machine_environment or {}, case_insensitive=case_insensitive)
    _overlay(environment, user_environment or {}, case_insensitive=case_insensitive)
    _overlay(environment, process_environment, case_insensitive=case_insensitive)
    return environment


def _windows_registry_environment(hive: object, path: str) -> dict[str, object]:
    try:
        import winreg

        values: dict[str, object] = {}
        with winreg.OpenKey(hive, path) as key:
            _, count, _ = winreg.QueryInfoKey(key)
            for index in range(count):
                name, value, _ = winreg.EnumValue(key, index)
                values[name] = value
        return values
    except (ImportError, OSError):
        return {}


def _expand_windows_variables(environment: dict[str, str]) -> dict[str, str]:
    lookup = {name.casefold(): value for name, value in environment.items()}

    def expand(value: str) -> str:
        return _PERCENT_VARIABLE.sub(lambda match: lookup.get(match.group(1).casefold(), match.group(0)), value)

    return {name: expand(value) for name, value in environment.items()}


def load_system_environment() -> dict[str, str]:
    """Return every available process, machine, and user environment variable.

    Values stay in-memory and are passed only to a child process; this function never logs them.
    """
    process_environment = dict(os.environ)
    if os.name != "nt":
        return merge_system_environment(process_environment=process_environment)

    try:
        import winreg
    except ImportError:
        return merge_system_environment(process_environment=process_environment)

    environment = merge_system_environment(
        process_environment=process_environment,
        machine_environment=_windows_registry_environment(winreg.HKEY_LOCAL_MACHINE, _WINDOWS_MACHINE_ENVIRONMENT),
        user_environment=_windows_registry_environment(winreg.HKEY_CURRENT_USER, _WINDOWS_USER_ENVIRONMENT),
    )
    return _expand_windows_variables(environment)
