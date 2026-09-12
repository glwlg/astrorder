"""Silently restart Astrorder through the verified, independent App lifecycle."""
from __future__ import annotations

import traceback
from pathlib import Path

from restart_service import restart

ROOT = Path(__file__).resolve().parent.parent


def main() -> None:
    try:
        restart()
    except Exception:  # pythonw has no console; keep the failure diagnosable.
        runtime = ROOT / ".runtime"
        runtime.mkdir(parents=True, exist_ok=True)
        with (runtime / "start-silent-error.log").open("a", encoding="utf-8") as log:
            traceback.print_exc(file=log)


if __name__ == "__main__":
    main()
