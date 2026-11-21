# 安装版构建与更新

星序安装版由 Wails 3 桌面客户端、Go 大内核和 Go 小内核组成。浏览器静态资源由 Go 大内核提供，不运行 Vite、FastAPI 或 Uvicorn。

## 构建候选安装器

在工具链准备完成后，从仓库根目录执行：

```sh
node scripts/build-installer.mjs
```

脚本重新构建 Go 构件、桌面绑定、前端和 Wails 程序，校验构件哈希后生成 `release/Astrorder-Setup-0.1.0.exe` 及安装器清单。公共构件暂存目录是 `.runtime/go-stage/`。

桌面内嵌前端使用 `ASTRORDER_WAILS=1` 构建，不注册 PWA worker；浏览器资源另行构建，包含 `sw-astrorder.js`。不能将两份产物混用。原生图片预览的 HTML 嵌入桌面 EXE，修改该页面时必须重新构建桌面程序。

桌面必须使用项目构建脚本，以保留 Wails 中文输入法修复；直接 `go build` 不包含该修复。

## 安装布局与数据

默认安装位置为 `%LOCALAPPDATA%\Programs\Astrorder`。安装时也可选择其他目录。安装目录包含以下文件：

- `Astrorder.exe`：桌面程序。
- `server/bin/`：大小内核、控制器和辅助程序。
- `server/frontend/dist/`：浏览器/PWA 静态资源。
- `server/connectors/`、`server/.hermes/`：Agent 安装资产。

用户状态目录为 `%LOCALAPPDATA%\Astrorder`。数据库、附件、配置、DPAPI 凭据和 WebView 状态与程序文件分开维护，不因源码清理或资源同步删除。DPAPI 凭据绑定 Windows 当前用户，复制文件不能代替账户间的凭据迁移。

## 大内核状态与重启

在 Windows PowerShell 中使用已安装的控制器：

```powershell
$AppPathKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\App Paths\Astrorder.exe'
$InstalledExe = (Get-Item -LiteralPath $AppPathKey).GetValue('')
$InstallDir = Split-Path -Parent $InstalledExe
$ServerRoot = Join-Path $InstallDir 'server'
$StateDir = Join-Path $env:LOCALAPPDATA 'Astrorder'
$Control = Join-Path $ServerRoot 'bin\astrorderctl.exe'
& $Control app status --root $ServerRoot --state-dir $StateDir
& $Control app restart --root $ServerRoot --state-dir $StateDir
```

只对明确归属的进程操作，核对 PID、创建时间、可执行文件路径和端口归属。大内核重启后核对健康状态、鉴权、静态资源与事件重连，同时确认小内核及其托管进程身份未变化。

## 发布边界

- **前端资源**：构建浏览器版本，使用 `astrorderctl sync --dev-root` 与 `--installed-root` 同步 `frontend/dist`，核对磁盘与 HTTP 返回资源哈希。
- **桌面程序**：备份旧文件，只替换并重启桌面进程，核对 EXE 哈希、`--check` 与窗口启动。不要按进程树结束小内核或 Agent。
- **大内核**：构建并验证后独立替换、重启，不捆绑小内核更新。发布前必须同时核对 `server/bin/helpers/linux-amd64/astrorder-helper` 和 `server/bin/helpers/linux-arm64/astrorder-helper` 及其 SHA-256；这些是远程文件、Git 和产物访问的运行依赖。缺失时从校验通过的 Go 阶段构件补齐 `helpers/`，不能只替换 `astrorder-app.exe`。发布后对已连接的 SSH/WSL 目标执行真实文件树读取，确认自动安装及绝对路径调用正常。
- **小内核**：只将验证后的候选暂存为同目录的 `astrorder-sessiond.exe.new`，核对哈希及 `update-check`；用户通过托盘手动激活。暂存不代表上线，Agent 不执行小内核停止、重启或更新。

仅构建或静态资源校验通过，不代表安装与原生交互已验收。每次交付以当次运行结果为准，不沿用历史报告中的进程号、哈希或测试数量。

在上述 PowerShell 中，若 `astrorderctl` 已加入 `PATH`，可在仓库的 `frontend` 目录构建并同步浏览器资源：

```powershell
npm run build:sync -- --installed-root "$ServerRoot"
```

## 隔离验证

Windows 安装器隔离验收使用 `node scripts/verify-installer-isolation.mjs`。开发壳使用独立状态目录和明确回环地址，禁止共用正式安装版状态。小内核发布约束以 [AGENTS.md](../AGENTS.md) 为准。
