"""Regenerate PWA icons from the shared transparent brand master."""
from pathlib import Path
import runpy

runpy.run_path(
    str(Path(__file__).resolve().parents[2] / 'scripts' / 'build_brand_icons.py'),
    run_name='__main__',
)
