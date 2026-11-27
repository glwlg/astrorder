# 构建、安装与更新

星序安装版由 **Wails 3 桌面客户端**、**Go 大内核** 与 **Go 小内核** 组成。Web / PWA 静态资源由 Go 大内核直接提供。

## 1. 构建安装器与产物

在工具链准备完成后（见 [`desktop-wails/README.md`](../desktop-wails/README.md)），从仓库根目录执行：

```sh
node scripts/build-installer.mjs
```

脚本会依次完成 Go 构件编译、桌面绑定生成、前端构建与 Wails 打包，核对构件哈希后在 `release/` 生成 Windows NSIS 安装器及清单；公共 Go 构件暂存于 `.runtime/go-stage/`。

构建注意事项：

- **桌面前端与 Web/PWA 分离**：桌面内嵌前端通过 `ASTRORDER_WAILS=1` 与 `ASTRORDER_OUT_DIR=desktop-wails/assets/app` 直接输出到桌面嵌入目录（禁用 PWA Service Worker，且不污染 `frontend/dist/`）；Web/PWA 静态资源通过 `npm --prefix frontend run build` 单独构建（包含 `sw-astrorder.js`），两者不可混用，构建脚本通过 `scripts/verify-frontend-dist.mjs` 强制校验。
- **使用项目构建脚本**：桌面程序必须通过 `scripts/desktop-wails.mjs` 构建以保留中文输入法修补；修改原生图片预览 HTML 后需重新构建桌面程序。

## 2. 安装目录与数据隔离

默认安装目录为 `%LOCALAPPDATA%\Programs\Astrorder`（安装时可自定义）：

- `Astrorder.exe`：Wails 3 桌面客户端。
- `server/bin/`：`astrorder-app.exe`（大内核）、`astrorder-sessiond.exe`（小内核）、`astrorderctl.exe`（服务控制器）及 `helpers/linux-{amd64,arm64}/astrorder-helper`（远程目标辅助程序）。
- `server/frontend/dist/`：Web / PWA 静态资源。
- `server/connectors/`、`server/.hermes/`：Agent 宿主插件资产。

用户数据独立存放于 `%LOCALAPPDATA%\Astrorder`（SQLite 数据库、附件、配置、DPAPI 凭据与 WebView 状态），程序升级或静态资源同步不会清理用户数据。

## 3. 服务控制与更新边界

在 Windows PowerShell 中可通过已安装的 `astrorderctl.exe` 查看与管理服务状态：

```powershell
$AppPathKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\App Paths\Astrorder.exe'
$InstalledExe = (Get-Item -LiteralPath $AppPathKey).GetValue('')
$InstallDir = Split-Path -Parent $InstalledExe
$ServerRoot = Join-Path $InstallDir 'server'
$StateDir = Join-Path $env:LOCALAPPDATA 'Astrorder'
$Control = Join-Path $ServerRoot 'bin\astrorderctl.exe'

& $Control service app status --root $ServerRoot --state-dir $StateDir
& $Control service app restart --root $ServerRoot --state-dir $StateDir
```

各组件独立发布边界：

- **Web / PWA 资源**：构建浏览器版 `frontend/dist/` 后，通过 `astrorderctl sync --dev-root <source> --installed-root <server>` 同步（或在 `frontend/` 执行 `npm run build:sync -- --installed-root "$ServerRoot"`）。同步命令会在拷贝前与落盘替换前两次校验 `index.html`、所有被引用的 `assets/*` 及 `sw-astrorder.js`，避免半成品覆盖线上静态目录；也可通过 `astrorderctl sync --verify-only --installed-root "$ServerRoot"` 或 `node scripts/verify-frontend-dist.mjs --mode browser "$ServerRoot/frontend/dist"` 单独核验。
- **桌面客户端**：使用 `node scripts/desktop-wails.mjs build-packaged` 构建正式版 `desktop-wails/bin/Astrorder.exe`（注入 `-X main.defaultPackaged=true`），并通过 `node scripts/deploy-local.mjs --desktop` 完成替换前 `--check` 门禁、替换重启与全链路验收；严禁将开发版 `astrorder-desktop-dev.exe` 覆盖至安装目录。
- **Go 大内核**：可独立替换并重启 `astrorder-app.exe`（通过 `node scripts/deploy-local.mjs --app` 或 `astrorderctl service app stop/start`），发布时同步核对 `helpers/linux-amd64/astrorder-helper` 与 `helpers/linux-arm64/astrorder-helper`；重启后确认健康检查正常且小内核 PID 未变化。
- **本地发布验收门禁**：随时可执行 `node scripts/verify-local-release.mjs` 一次性校验 Web Dist、`Astrorder.exe --check`（`packaged: true` 与生产 `stateDir`）、桌面主窗口状态、大内核健康态及小内核未受扰动。
- **Go 小内核**：将验证后的新版本暂存为同目录的 `astrorder-sessiond.exe.new`，通过 `astrorderctl service daemon update-check` 核对后，由用户在系统托盘 **「小内核 → 更新小内核」** 手动激活，遵循 [`AGENTS.md`](../AGENTS.md) 约束。

### 小内核运行环境变量参考

`astrorderctl` 启动 `astrorder-sessiond` 时通过以下环境变量指定路径与凭据（配置格式见 `session-daemon-go/internal/configuration/config.go`）：

- `ASTRORDER_SESSION_DAEMON_EXECUTABLE`：小内核二进制绝对路径
- `ASTRORDER_SESSION_DAEMON_CONFIG`：运行时 JSON 配置文件绝对路径
- `ASTRORDER_SESSION_DAEMON_DB`：小内核 SQLite 数据库绝对路径
- `ASTRORDER_SESSION_DAEMON_SECRET`：内部密钥
- `ASTRORDER_SESSION_DAEMON_GRPC_TARGET`：显式回环地址与非零端口
- `ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL`：gRPC 调用凭据

## 4. 隔离验收

- 安装器隔离验收：`node scripts/verify-installer-isolation.mjs`
- 隔离 HTTP 验收：`node scripts/migration/isolated-http-verify.mjs <repo-abs-path> <temp-dir>`
- 小内核真实隔离重启测试：设置 `ASTRORDER_TEST_DAEMON_EXECUTABLE` 后在 `app-server-go/` 执行 `go test ./cmd/astrorder-app -run TestDaemonRestartWithRealIsolatedLifecycle -count=1`
