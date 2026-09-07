# 星序 · Astrorder

独立、跨设备的 Hermes / Codex 工作台。当前可验证的是本地 FastAPI、内置 SPA、认证事件协议和隔离连接器的完整链路；原生 Hermes/Codex 会话尚未获得实机授权验证，不能视为已接通生产 Agent。详见 `docs/reports/integration.md`。

## 栈

- React / TypeScript / Vite + Mantine + TanStack Query + Zustand
- FastAPI / Pydantic / SQLAlchemy / SQLite / Uvicorn
- Vitest / Playwright、pytest / Ruff

## 前置条件

- Python 3.11 与 `uv`
- Node/npm
- 生产和浏览器联调都只绑定回环地址；不要把服务暴露到公网。
- 设置不同的 `ASTRORDER_BROWSER_SECRET` 与 `ASTRORDER_CONNECTOR_SECRET`。没有浏览器密钥时，私有 HTTP 与 WebSocket 路由会失败关闭。

安装开发依赖：

    cd backend
    uv sync --extra dev

    cd ../frontend
    npm ci

## 本地开发

后端在 Git Bash 中启动。用实际私密值替换尖括号内容，不要把值写入仓库：

    cd backend
    ASTRORDER_HOST=127.0.0.1 \
    ASTRORDER_PORT=8765 \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:5173,http://localhost:5173' \
    uv run astrorder-server

另一个终端启动 Vite：

    cd frontend
    npm run dev

访问 `http://127.0.0.1:5173`。Vite 会代理 `/api`、`/health` 与 `/ws` 到 8765；浏览器凭证只进入 HttpOnly Cookie，不应出现在 URL 或 localStorage。

## 单进程本地部署

先构建前端，再让 FastAPI 在同一回环 origin 服务 SPA、API 和 WebSocket：

    cd frontend
    npm run build

    cd ../backend
    ASTRORDER_HOST=127.0.0.1 \
    ASTRORDER_PORT=8765 \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:8765' \
    ASTRORDER_STATIC_DIR='../frontend/dist' \
    uv run astrorder-server

`/chat`、`/monitor`、`/agents` 会回退到已构建的 SPA；未知 `/api/...` 仍返回 404。附件下载经过认证，并且上传文件名、类型、大小和存储路径都受服务端校验。

## 验证

基础检查：

    cd backend
    uv run pytest -q
    uv run ruff check .
    uv run ruff check ../connectors --output-format=concise
    uv run ruff check ../scripts --output-format=concise

    cd ../frontend
    npm run test
    npm run lint
    npm run build

真实浏览器联调必须使用私有临时 SQLite、测试密钥和 `scripts/inert_connector.py`，而不是用户会话。先按“单进程本地部署”启动隔离服务，然后在第二个终端启动测试夹具：

    cd backend
    ASTRORDER_CONNECTOR_ENDPOINT='ws://127.0.0.1:8765/ws/v1/connector' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    uv run python ../scripts/inert_connector.py

Playwright 默认不下载浏览器。若本机已有 Chromium/Chrome，指定其可执行文件后运行：

    cd frontend
    ASTRORDER_E2E_BASE_URL='http://127.0.0.1:8765' \
    ASTRORDER_E2E_TOKEN='<browser-secret>' \
    ASTRORDER_E2E_EXECUTABLE='C:/path/to/chrome.exe' \
    npm run test:e2e

安全门禁脚本针对同一隔离服务运行，结果可写入 `.runtime/`：

    cd backend
    ASTRORDER_TEST_BASE_URL='http://127.0.0.1:8765' \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_EVIDENCE_PATH='../.runtime/security-gates.json' \
    uv run python ../scripts/verify_isolated_security.py

## 原生连接器状态

`connectors/hermes` 和 `connectors/codex` 是可构建的独立工件，使用已记录的 Hermes 插件钩子与 Codex app-server JSON-RPC；它们不会自动安装、启用、读取凭证、重启 Desktop 或向现有会话发送提示。安装/启用 Hermes 插件、配置 Codex companion、以及对隔离原生会话的非破坏性验证都需要操作者明确授权。

受控运行时页面刻意保持不可用：单独启动 `codex app-server` 不等于连接了 Astrorder Agent。原生能力限制、已验证协议和待办项见 `connectors/README.md` 与 `docs/reports/integration.md`。

## 约定与报告

- 产品目标：`docs/PRODUCT.md`
- 协议：`docs/CONTRACT.md`
- 联调结果与限制：`docs/reports/integration.md`
- 原 `Hermes-plugins/overlook` 仅作只读交互参考，绝不修改运行中的中继或 Desktop 插件。
