from __future__ import annotations

import uvicorn

from .config import Settings


def main() -> None:
    settings = Settings.from_env()
    uvicorn.run("astrorder.main:app", host=settings.host, port=settings.port)


if __name__ == "__main__":
    main()
