"""Print the browser token for an explicit desktop copy action."""
import os

from run_production import load_production_environment

load_production_environment()
print(os.environ["ASTRORDER_BROWSER_SECRET"], end="")
