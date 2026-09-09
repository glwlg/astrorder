# 星序 · Astrorder

独立、跨设备的 Hermes / Codex 工作台。本机 Hermes 已通过受控、全新的 TUI-gateway 会话完成实机验证；SSH 远程配置界面已实现，但没有用户提供的远端主机，因此未声明远端已连接。Codex 原生生命周期仍未验证。详见 `docs/reports/hermes-local-ssh.md`。

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
    ASTRORDER_PORT=30002 \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:30001,http://localhost:30001' \
    uv run astrorder-server

另一个终端启动 Vite：

    cd frontend
    npm run dev

访问 `http://127.0.0.1:30001`。Vite 会代理 `/api`、`/health` 与 `/ws` 到 30002；浏览器凭证只进入 HttpOnly Cookie，不应出现在 URL 或 localStorage。5173 已保留给其他项目，Astrorder 不会使用它。

## 单进程本地部署

先构建前端，再让 FastAPI 在同一回环 origin 服务 SPA、API 和 WebSocket：

    cd frontend
    npm run build

    cd ../backend
    ASTRORDER_HOST=127.0.0.1 \
    ASTRORDER_PORT=30002 \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:30002' \
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
    ASTRORDER_CONNECTOR_ENDPOINT='ws://127.0.0.1:30002/ws/v1/connector' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    uv run python ../scripts/inert_connector.py

Playwright 默认不下载浏览器。若本机已有 Chromium/Chrome，指定其可执行文件后运行：

    cd frontend
    ASTRORDER_E2E_BASE_URL='http://127.0.0.1:30002' \
    ASTRORDER_E2E_TOKEN='<browser-secret>' \
    ASTRORDER_E2E_EXECUTABLE='C:/path/to/chrome.exe' \
    npm run test:e2e

安全门禁脚本针对同一隔离服务运行，结果可写入 `.runtime/`：

    cd backend
    ASTRORDER_TEST_BASE_URL='http://127.0.0.1:30002' \
    ASTRORDER_BROWSER_SECRET='<browser-secret>' \
    ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
    ASTRORDER_EVIDENCE_PATH='../.runtime/security-gates.json' \
    uv run python ../scripts/verify_isolated_security.py

## 本机 Hermes 与 SSH 远程设置

在 `/agents` 的“本机 Hermes”卡片中，已认证的操作者可以启动连接。服务端只通过已知可执行路径与 `hermes --version` 发现本机运行时；浏览器从不填写连接器 WebSocket 地址或密钥。连接操作会：

1. 仅在当前 Hermes profile 的 `plugins/astrorder-hermes` 写入经过一致性检查的 Astrorder wrapper，并通过 `hermes plugins enable astrorder-hermes` 启用；不修改 Hermes core、其他 profile、模型设置、Desktop、gateway 或 Overlook。
2. 启动 Astrorder 拥有的 `tui_gateway.entry` 原生运行时和一个全新的 `source: local` 会话。连接器密钥只作为该子进程环境变量传递，不写入浏览器、URL 或配置文件。
3. 将 Astrorder-owned 会话的文本命令交给 Hermes TUI gateway 的 `prompt.submit`；返回 `accepted` 只代表原生 runtime 接受调度，绝不伪造 `completed`。断线或无法确认的投递保留为 `unknown`，不自动重发。

预览中可显式设置 `ASTRORDER_AUTO_CONNECT_LOCAL_HERMES=true`，使 `.runtime/start_preview.py` 在回环端口 30002 启动后连接本机 Hermes。它不是默认行为；脚本生成的浏览器凭证保持本地，不要打印或提交。若要移除这个可逆 profile wrapper，请先断开 Astrorder，然后在同一 Hermes profile 运行 `hermes plugins disable astrorder-hermes` 与 `hermes plugins remove astrorder-hermes`。

SSH v1 只保存非敏感设置：主机或 SSH config alias、端口、用户、identity-file 引用、远端 Hermes 路径和工作区。它不保存密码或私钥内容，不关闭 host-key verification，也不会拼接 shell 字符串。测试按钮只执行本地 `ssh -G` 配置校验，不接触远程主机；“连接远程 Hermes”在远端原生插件尚未由操作者配置时明确返回未建立连接。要继续，提供一个可信的 SSH host/alias、已验证的 host key、可用 OpenSSH/agent 身份、远端 Hermes 可执行路径和工作区，并获得远端安装/启动授权。

`connectors/hermes` 和 `connectors/codex` 仍是独立工件。Codex 的 app-server lifecycle、任意既有 Hermes 会话和远端 SSH host 都未由本次验证访问。原生能力限制、已验证协议和待办项见 `connectors/README.md` 与 `docs/reports/hermes-local-ssh.md`。

## 约定与报告

- 产品目标：`docs/PRODUCT.md`
- 协议：`docs/CONTRACT.md`
- 联调结果与限制：`docs/reports/integration.md`
- 本机 Hermes / SSH 实机结果与限制：`docs/reports/hermes-local-ssh.md`
- 原 `Hermes-plugins/overlook` 仅作只读交互参考，绝不修改运行中的中继或 Desktop 插件。
