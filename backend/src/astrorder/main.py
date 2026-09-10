from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
from starlette.exceptions import HTTPException as StarletteHTTPException

from .api import router
from .auth import authorize_browser_websocket, authorize_connector_websocket
from .config import Settings
from .connections import ConnectionController
from .native_codex import CodexConnection
from .environment_connections import EnvironmentConnections
from .events import EventHub
from .runtime import ProcessSupervisor
from .schemas import AgentModel
from .service import ControlService, ProtocolError
from .store import Store

logger = logging.getLogger(__name__)


class SpaStaticFiles(StaticFiles):
    """Serve compiled client routes without turning unknown API paths into HTML."""

    async def get_response(self, path: str, scope):
        try:
            return await super().get_response(path, scope)
        except StarletteHTTPException as exc:
            request_path = str(scope.get("path", ""))
            reserved = request_path in {"/api", "/ws", "/health"} or request_path.startswith(
                ("/api/", "/ws/")
            )
            if exc.status_code != 404 or reserved:
                raise
            return await super().get_response("index.html", scope)


def _resync_event(cursor: int, reason: str) -> dict[str, object]:
    return {
        "id": f"resync-{cursor}",
        "cursor": cursor,
        "type": "resync_required",
        "agent_id": None,
        "session_id": None,
        "data": {"reason": reason},
    }


def create_app(settings: Settings | None = None) -> FastAPI:
    runtime_settings = settings or Settings.from_env()

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        store = Store(runtime_settings)
        hub = EventHub()
        service = ControlService(store, hub, runtime_settings)
        from .attachments import AttachmentManager

        app.state.settings = runtime_settings
        app.state.store = store
        app.state.hub = hub
        app.state.service = service
        service.mark_persisted_connectors_disconnected()
        store.mark_ssh_connections_disconnected()
        app.state.attachments = AttachmentManager(runtime_settings, store)
        app.state.supervisor = ProcessSupervisor(runtime_settings)
        app.state.connections = ConnectionController(runtime_settings, store)
        app.state.codex = CodexConnection(runtime_settings, store, service)
        app.state.environments = EnvironmentConnections(runtime_settings, store, service, app.state.connections, app.state.codex)
        from .hermes_approvals import HermesApprovals
        service.hermes_approvals = HermesApprovals(app.state.connections, service)
        from .native_observers import NativeObservers
        observers = NativeObservers(app)
        app.state.observers = observers
        observer_task = asyncio.create_task(observers.run())
        restore_task = None
        if runtime_settings.auto_connect_local_hermes:
            restore_task = asyncio.create_task(asyncio.to_thread(app.state.environments.restore))
        try:
            yield
        finally:
            observers.stopping.set()
            await observer_task
            if restore_task is not None:
                await restore_task
            await asyncio.to_thread(app.state.codex.disconnect)
            await asyncio.to_thread(app.state.environments.shutdown)
            app.state.connections.shutdown(service)
            await service.shutdown()
            await app.state.supervisor.shutdown()
            store.close()

    app = FastAPI(title="星序 · Astrorder", version="0.1.0", lifespan=lifespan)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=list(runtime_settings.allowed_origins),
        allow_origin_regex=r"^https?://(localhost|127\.0\.0\.1|192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)(:\d+)?$",
        allow_credentials=True,
        allow_methods=["GET", "POST", "PUT", "DELETE", "OPTIONS"],
        allow_headers=["Authorization", "Content-Type"],
    )
    app.include_router(router)

    @app.get("/health")
    def health() -> dict[str, str | int]:
        return {"status": "ok", "service": "astrorder", "protocol_version": 1}

    @app.websocket("/ws/v1/events")
    async def browser_events(websocket: WebSocket) -> None:
        if not authorize_browser_websocket(websocket, runtime_settings):
            await websocket.close(code=4401)
            return
        raw_after = websocket.query_params.get("after", "0")
        try:
            after = int(raw_after)
            if after < 0:
                raise ValueError
        except ValueError:
            await websocket.close(code=1008, reason="Invalid cursor")
            return
        queue = websocket.app.state.hub.subscribe()
        try:
            await websocket.accept()
            resync, replay = websocket.app.state.store.replay(after)
            last_cursor = after
            if resync:
                cursor = websocket.app.state.store.latest_cursor()
                await websocket.send_json(_resync_event(cursor, "cursor_out_of_range"))
                last_cursor = cursor
            else:
                for event in replay:
                    await websocket.send_json(event)
                    last_cursor = max(last_cursor, int(event["cursor"]))
            while True:
                event = await queue.get()
                cursor = int(event.get("cursor", 0))
                if cursor <= last_cursor:
                    continue
                await websocket.send_json(event)
                last_cursor = cursor
        except WebSocketDisconnect:
            pass
        finally:
            websocket.app.state.hub.unsubscribe(queue)

    @app.websocket("/ws/v1/connector")
    async def connector(websocket: WebSocket) -> None:
        if not authorize_connector_websocket(websocket, runtime_settings):
            await websocket.close(code=4401)
            return
        await websocket.accept()
        connection = None
        try:
            hello = await websocket.receive_json()
            if (
                not isinstance(hello, dict)
                or hello.get("type") != "hello"
                or hello.get("protocol_version") != 1
            ):
                raise ProtocolError("Connector hello is invalid")
            agent = AgentModel.model_validate(hello.get("agent", {})).model_dump()
            connection = await websocket.app.state.service.register_connector(websocket, agent)
            while True:
                frame = await websocket.receive_json()
                if not isinstance(frame, dict):
                    raise ProtocolError("Connector frame is invalid")
                frame_type = frame.get("type")
                if frame_type == "event":
                    event = frame.get("event")
                    if not isinstance(event, dict):
                        raise ProtocolError("Connector event is invalid")
                    websocket.app.state.service.accept_connector_event(agent["id"], event)
                elif frame_type == "pull":
                    await websocket.app.state.service._dispatch_queued(connection)
                elif frame_type == "ping":
                    await connection.send({"type": "pong"})
                else:
                    await connection.send({"type": "error", "detail": "Unsupported connector frame"})
        except WebSocketDisconnect:
            pass
        except (ProtocolError, ValueError) as exc:
            try:
                await websocket.send_json({"type": "error", "detail": str(exc)})
            except (RuntimeError, WebSocketDisconnect) as send_exc:
                logger.debug("connector error frame could not be sent: %s", send_exc)
        finally:
            if connection is not None:
                await websocket.app.state.service.disconnect(connection, "Connector disconnected")
            try:
                await websocket.close()
            except (RuntimeError, WebSocketDisconnect) as close_exc:
                logger.debug("connector websocket was already closed: %s", close_exc)

    static_dir = runtime_settings.static_dir
    if static_dir is None:
        static_dir = Path(__file__).resolve().parents[3] / "frontend" / "dist"
    if (static_dir / "index.html").is_file():
        app.mount("/", SpaStaticFiles(directory=static_dir, html=True), name="frontend")

    @app.exception_handler(RequestValidationError)
    async def validation_error(_request, _exc) -> JSONResponse:
        return JSONResponse(status_code=422, content={"detail": "Invalid request"})

    @app.exception_handler(Exception)
    async def safe_error(_request, _exc) -> JSONResponse:
        return JSONResponse(status_code=500, content={"detail": "Internal server error"})

    return app


app = create_app()
