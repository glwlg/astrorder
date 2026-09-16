"""Start the login-owned Session Daemon and exit."""
from production_daemon import ensure_production_daemon
from run_production import ROOT, load_production_environment

if __name__ == "__main__":
    ensure_production_daemon(load_production_environment(), root=ROOT)
