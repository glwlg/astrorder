# Single-service production deployment (Windows)

The production service uses one Uvicorn worker, without reload or Vite. FastAPI serves the compiled `frontend/dist` assets, API, and WebSocket on port 30001. Agent connector subprocesses remain separate native runtimes; they are not additional web workers.

## Start

From the repository root, under the same Windows account used for deployment:

```bash
backend/.venv/Scripts/python.exe scripts/run_production.py
```

Do not start a second instance while port 30001 is occupied. The entrypoint loads `.runtime/production.json` and `.runtime/production.credentials.dpapi`. Both are ignored by Git. The latter uses Windows current-user DPAPI; copying it to a different account or machine is not a credential migration method.

## Update

1. Build `frontend/dist` with `npm run build` from `frontend/`.
2. Back up the configured SQLite database with SQLite's backup API.
3. Identify the exact current process by port 30001, repository cwd, and `scripts/run_production.py` command line.
4. Preserve selected Agent connections and wait for user work to become idle. Close owned connectors before stopping that exact service.
5. Start the entrypoint and verify health, authenticated bootstrap, native IDs, selected connections, static routes, and WebSocket on 30001.

The old development restart scripts targeting 30002 are not appropriate for this deployment. Do not restart Vite.

## Local settings

`.runtime/production.json` contains nonsecret `ASTRORDER_*` environment settings, including database and attachment locations, static asset directory, port, and explicit allowed origins. Private domain names belong here, not in tracked source. Vite's optional `frontend/vite.hosts.local.json` is development-only and not used by the production service.

Logs are stored in `.runtime/production.log`; deployment backups and readback results are also under `.runtime/`.

The web listener is loopback-only. Existing reverse proxies or access-control gateways should forward to `127.0.0.1:30001`, including WebSocket upgrades. Keep their access protection enabled. Automatic startup at Windows login/boot is not installed by this entrypoint.
