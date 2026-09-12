from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager, suppress
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
from .connections import ConnectionController, ConnectionError
from .environment_connections import EnvironmentConnections
from .events import EventHub
from .native_codex import CodexConnection
from .runtime import ProcessSupervisor
from .schemas import AgentModel
from .service import ControlService, ProtocolError
from .store import Store

logger = logging.getLogger(__name__)


async def restore_hermes_model(
    app, agent_id: str, session_id: str, provider: str, model: str, effort: str | None = None
) -> None:
    runtime = app.state.connections.get_runtime_by_agent_id(agent_id)
    if runtime is None:
        raise RuntimeError("Hermes runtime is unavailable")
    current = await asyncio.to_thread(runtime.model, session_id)
    model_changed = current.get("provider") != provider or current.get("model") != model
    if model_changed:
        result = await asyncio.to_thread(runtime.set_model, session_id, provider, model)
        if not isinstance(result, dict) or result.get("provider") != provider or result.get("model") != model:
            raise RuntimeError("Hermes model readback did not match the saved binding")
    if effort is not None and (model_changed or current.get("effort") != effort):
        result = await asyncio.to_thread(runtime.set_effort, session_id, effort)
        if not isinstance(result, dict) or result.get("effort") != effort:
            raise RuntimeError("Hermes reasoning readback did not match the saved binding")


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
        daemon_stopping = asyncio.Event()
        daemon_task: asyncio.Task[None] | None = None
        app.state.daemon_bridge = None
        if runtime_settings.session_daemon_enabled:
            from .daemon.bridge import DaemonBridge

            app.state.daemon_bridge = DaemonBridge(
                store,
                service,
                runtime_settings.session_daemon_endpoint,
                secret=runtime_settings.session_daemon_secret,
                request_timeout=runtime_settings.session_daemon_request_timeout,
            )
            daemon_task = asyncio.create_task(app.state.daemon_bridge.run(daemon_stopping))
        app.state.daemon_terminal_relay = None
        app.state.daemon_pty_unregistrations = []
        if runtime_settings.daemon_pty_enabled:
            if app.state.daemon_bridge is None:
                raise ValueError("daemon PTY requires the Session Daemon bridge")
            from .daemon.terminal_relay import DaemonTerminalRelay

            app.state.daemon_terminal_relay = DaemonTerminalRelay(
                app.state.daemon_bridge,
                secret=runtime_settings.session_daemon_secret,
            )
            app.state.daemon_pty_unregistrations = [
                app.state.daemon_bridge.register_native_frame_handler(
                    "pty.output", lambda _session_id, _payload: None
                ),
                app.state.daemon_bridge.register_native_frame_handler(
                    "pty.closed", lambda _session_id, _payload: None
                ),
            ]
        app.state.hermes_command_frame_router = None
        if runtime_settings.daemon_hermes_enabled or runtime_settings.daemon_ssh_enabled:
            if app.state.daemon_bridge is None:
                raise ValueError("daemon Hermes projection requires the Session Daemon bridge")
            from .daemon.hermes_projection import HermesCommandFrameRouter
            from .daemon.hermes_compaction_projection import HermesCompactionFrameRouter

            app.state.hermes_compaction_frame_router = HermesCompactionFrameRouter(
                app.state.daemon_bridge, store, service,
            )

            app.state.hermes_command_frame_router = HermesCommandFrameRouter(
                app.state.daemon_bridge,
                store,
                service,
            )
        service.mark_persisted_connectors_disconnected()
        store.mark_ssh_connections_disconnected()
        app.state.attachments = AttachmentManager(runtime_settings, store)
        app.state.supervisor = ProcessSupervisor(runtime_settings)
        app.state.daemon_hermes_controller = None
        local_hermes_controller = None
        if runtime_settings.daemon_hermes_enabled:
            if app.state.daemon_bridge is None:
                raise ValueError("daemon Hermes requires the Session Daemon bridge")
            from .daemon.hermes_control import DaemonHermesController

            local_hermes_controller = DaemonHermesController(app.state.daemon_bridge)
            app.state.daemon_hermes_controller = local_hermes_controller
        app.state.daemon_ssh_factory = None
        daemon_ssh_factory = None
        if runtime_settings.daemon_ssh_enabled:
            if app.state.daemon_bridge is None:
                raise ValueError("daemon SSH requires the Session Daemon bridge")
            from .daemon.ssh_control import DaemonSshController

            def daemon_ssh_factory(row):
                connection_id = row.get("id") if isinstance(row, dict) else None
                ssh_settings = row.get("settings") if isinstance(row, dict) else None
                if not isinstance(connection_id, str) or not connection_id or not isinstance(ssh_settings, dict):
                    raise ValueError("saved SSH connection identity is invalid")
                return DaemonSshController(
                    app.state.daemon_bridge,
                    connection_id=connection_id,
                    ssh_settings=ssh_settings,
                )

            app.state.daemon_ssh_factory = daemon_ssh_factory
        app.state.connections = ConnectionController(
            runtime_settings,
            store,
            local_controller=local_hermes_controller,
            daemon_ssh_factory=daemon_ssh_factory,
        )
        app.state.codex = CodexConnection(runtime_settings, store, service)
        app.state.codex_native_frame_router = None
        daemon_codex_controller_factory = None
        if runtime_settings.daemon_codex_enabled:
            if app.state.daemon_bridge is None:
                raise ValueError("daemon Codex requires the Session Daemon bridge")
            from .daemon.codex_control import DaemonCodexController
            from .daemon.codex_projection import CodexNativeFrameRouter

            codex_native_frame_router = CodexNativeFrameRouter(app.state.daemon_bridge)
            app.state.codex_native_frame_router = codex_native_frame_router
            def daemon_codex_controller_factory(connection):
                return DaemonCodexController(
                    app.state.daemon_bridge,
                    codex_native_frame_router,
                    connection,
                )

            app.state.codex.set_daemon_controller_factory(daemon_codex_controller_factory)
        app.state.environments = EnvironmentConnections(
            runtime_settings,
            store,
            service,
            app.state.connections,
            app.state.codex,
            daemon_codex_controller_factory=daemon_codex_controller_factory,
        )
        service.hermes_model_restorer = lambda agent_id, session_id, provider, model, effort: restore_hermes_model(
            app, agent_id, session_id, provider, model, effort
        )
        from .hermes_approvals import HermesApprovals
        service.hermes_approvals = HermesApprovals(app.state.connections, service)
        from .native_observers import NativeObservers
        observers = NativeObservers(app)
        app.state.observers = observers
        service.observer_approvals = observers
        observer_task = asyncio.create_task(observers.run())
        restore_tasks: list[asyncio.Task[None]] = []
        if runtime_settings.daemon_codex_enabled:
            async def restore_daemon_codex() -> None:
                try:
                    await asyncio.to_thread(app.state.codex.connect)
                except ConnectionError as exc:
                    logger.warning("daemon Codex projection restore failed: %s", exc)

            restore_tasks.append(asyncio.create_task(restore_daemon_codex()))
        if runtime_settings.auto_connect_local_hermes:
            restore_tasks.append(
                asyncio.create_task(asyncio.to_thread(app.state.environments.restore))
            )
        try:
            yield
        finally:
            if app.state.hermes_command_frame_router is not None:
                app.state.hermes_command_frame_router.close()
                app.state.hermes_compaction_frame_router.close()
            for unregister in app.state.daemon_pty_unregistrations:
                unregister()
            daemon_stopping.set()
            if daemon_task is not None:
                try:
                    await asyncio.wait_for(daemon_task, timeout=0.5)
                except TimeoutError:
                    daemon_task.cancel()
                    with suppress(asyncio.CancelledError):
                        await daemon_task
            observers.stopping.set()
            await observer_task
            for restore_task in restore_tasks:
                await restore_task
            await asyncio.to_thread(app.state.codex.disconnect)
            if app.state.codex_native_frame_router is not None:
                app.state.codex_native_frame_router.close()
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
        allow_methods=["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
        allow_headers=["Authorization", "Content-Type"],
    )
    app.include_router(router)

    @app.get("/health")
    def health() -> dict[str, str | int]:
        return {"status": "ok", "service": "astrorder", "protocol_version": 1}

    @app.websocket("/ws/v1/terminal")
    async def terminal_websocket(websocket: WebSocket) -> None:
        if not authorize_browser_websocket(websocket, runtime_settings):
            await websocket.close(code=4401)
            return

        session_id = websocket.query_params.get("session_id")
        session_obj = None
        workspace_dir = None
        ssh_argv = None
        remote_workspace = None

        if session_id:
            try:
                session_obj = websocket.app.state.store.find_session_by_id(session_id)
                if session_obj:
                    cid = session_obj.get("connection_id")
                    if cid and cid != "local":
                        # 这是一个 SSH 远程会话
                        ssh_conn = websocket.app.state.store.get_ssh_connection(cid)
                        if ssh_conn:
                            from .ssh_transport import SshNativeRuntime
                            runtime = SshNativeRuntime(
                                ssh_conn["settings"],
                                ssh_conn["id"],
                                0,
                                None,
                                None,
                                connector_secret=None,
                            )
                            # 构建带 -tt 交互式伪终端分配的 SSH 命令
                            base = [a for a in runtime._base_ssh_argv() if a != "-T"]
                            ssh_argv = base + ["-tt", runtime._target()]
                            remote_workspace = session_obj.get("workspace")
                    elif session_obj.get("workspace"):
                        workspace_dir = session_obj["workspace"]
            except Exception:
                pass

        relay = websocket.app.state.daemon_terminal_relay
        if relay is not None and isinstance(session_obj, dict):
            from .daemon.terminal_relay import daemon_pty_target

            target = daemon_pty_target(session_obj)
            if target is not None:
                agent_id, native_session_id, workspace = target
                await relay.serve(
                    websocket,
                    agent_id=agent_id,
                    session_id=native_session_id,
                    workspace=workspace,
                )
                return

        await websocket.accept()
        from .terminal_service import TerminalSession

        term = TerminalSession(
            workspace=workspace_dir,
            ssh_argv=ssh_argv,
            remote_workspace=remote_workspace,
        )
        try:
            await term.start()
        except Exception as exc:
            await websocket.send_text(f"\r\n\x1b[31m启动终端失败: {exc}\x1b[0m\r\n")
            await websocket.close()
            return

        # 必须在一个专用线程中执行阻塞式 PTY 读取，避免阻塞 asyncio 事件循环
        async def read_pty_loop():
            loop = asyncio.get_running_loop()
            try:
                while True:
                    # 阻塞式从 PTY 读入数据
                    chunk = await loop.run_in_executor(None, term.read_sync)
                    if not chunk:
                        break
                    await websocket.send_text(chunk)
            except Exception:
                pass

        read_task = asyncio.create_task(read_pty_loop())

        try:
            while True:
                msg = await websocket.receive_text()
                # 支持控制包（如 resize）或普通键盘键入
                if msg.startswith("{\"type\":\"resize\""):
                    try:
                        import json
                        data = json.loads(msg)
                        term.resize(int(data.get("cols", 80)), int(data.get("rows", 24)))
                    except Exception:
                        pass
                else:
                    term.write_sync(msg)
        except (WebSocketDisconnect, Exception):
            pass
        finally:
            read_task.cancel()
            await term.close()

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
