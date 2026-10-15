# Wails 3 桌面开发框架

本目录是星序桌面壳迁移的可运行起点，复用 `frontend/`，不维护第二套业务 UI。完整规范见 [迁移文档](../docs/design/astrorder-wails3-migration.md)，接手任务见 [开发 Goal](../docs/design/astrorder-wails3-development-goal.md)。

## 当前边界

桌面能力已接线；实际验收边界以报告为准：
- 独立 Go module 与 Windows 原生绑定已接管 `window.astrorderDesktop` 全量 API 契约（W01–W16）。
- 支持系统托盘、双击还原、单实例互斥唤醒、关闭隐藏至托盘、显式退出。
- 后台服务生命周期编排：集成 `astrorderctl`，实现双内核独立管理，退出客户端时停止大内核，Session Daemon 保持独立运行。
- Windows 注册表自启配置（W02）、系统原生通知（W05）、剪贴板文件写入（W06）、可信原生文件/目录选择与保存对话框（W07/W09）。
- 前端集中适配层 `frontend/src/desktop/adapter.ts` 解决 WebView2 WebSocket 直连本地端口机制。
- 完整产出 NSIS 候选安装器 `release/Astrorder-Setup-0.1.0.exe`，已在隔离环境完成全新安装、覆盖升级、双内核生命周期独立性以及保留用户数据的静默卸载验证。详细报告见 [验收报告](../docs/reports/astrorder-wails3-acceptance.md)。

## 手动更新小内核

托盘菜单「小内核 → 更新小内核」检查已安装 `astrorder-sessiond.exe` 同目录的 `astrorder-sessiond.exe.new`。没有候选时提示「没有可用的小内核更新」，不停止服务。有候选时校验文件，沿用活动会话的原生确认，再由独立 `astrorderctl` 完成停止旧内核、替换文件、启动新内核。新内核启动失败会恢复旧文件并尝试启动旧内核。

控制器不依赖 Agent 会话或客户端的等待上下文，输出保存在状态目录的 `daemon-update-*.log`。成功后保留旧 EXE 备份，移除已应用的 `.new`。此更新会中断旧内核托管的运行任务；准备候选文件不代表已经启用更新。

## 工具链

2026-10-12 查询并安装：Go `1.27.2`、Node `26.11.1`、Wails Go/CLI `v3.0.0-beta.28`、`@wailsio/runtime` `3.0.0-beta.28`。以 `toolchain.json`、`go.mod/go.sum`、`frontend/package.json/package-lock.json` 为准。前端直接依赖已精确锁定，包含 React `19.3.0`、TypeScript `7.0.2`、Vite `8.3.4`、Mantine `9.7.2`。

当前脚本只支持 Windows x64。Go 命令需在 PATH；专用 Node/npm 与 Wails CLI 位于被忽略的 `.tools/wails/`。运行需要 WebView2，本机 smoke 已证明可用。NSIS 通过 `scripts/install-nsis.ps1` 安装；安装器只检测并提示缺失的 WebView2，不自动安装运行时。

## 首次安装及复现

仓库根目录 PowerShell 执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-desktop-wails.ps1
```

脚本使用锁定版本，缺少 Node 时从官网下载安装并校验 SHA256，缺少 Wails CLI 时执行精确 tag 的 `go install`，随后运行 `go mod download` 和 `npm ci`。不会启动内核或访问生产配置。后续升级工具链须重新核对官方版本及兼容性，并同步修改锁文件。

## 开发命令

仓库根目录执行。以下使用专用 Node，不依赖全局 Node 版本：

```powershell
$Node = '.\.tools\wails\node-v26.11.1-win-x64\node.exe'
& $Node scripts/desktop-wails.mjs build
& $Node scripts/desktop-wails.mjs check
& $Node scripts/desktop-wails.mjs test
& $Node scripts/desktop-wails.mjs smoke
& $Node scripts/desktop-wails.mjs run
```

- `install`：从 Go/npm 锁文件安装依赖。
- `bindings`：重新生成 `assets/bindings/`；保留生成文件，禁止手工修改。
- `build`：生成 Windows 图标/DPI manifest 的 syso → bindings → 类型检查/构建现有 React → 复制到 `assets/app/` → Go `-trimpath -tags production -H=windowsgui` 构建 → EXE `--check`。
- `check`：版本、Go module 校验、桌面 Go 测试/vet、Node 文件路径/预览测试、前端 lint。现有 lint 警告不代表已清零。
- `test`：现有前端全量 Vitest。
- `smoke`：真实 WebView 启动，页面调用 Go，读回 `bin/smoke-report.json`，随后退出；只验证开发入口绑定，不能替代业务验收。
- `run`：运行已构建 EXE，后续参数直接传给 EXE；改源码后先 build。

构建产物：`desktop-wails/bin/astrorder-desktop-dev.exe`。工具目录、`bin/`、复制的 `assets/app/` 不入 Git；从干净检出运行 Go 测试前先 build，以提供嵌入 React 资产。

若已有单独启动的隔离 App Server，可显式连接：

```powershell
& $Node scripts/desktop-wails.mjs run --backend http://127.0.0.1:30102 --state-dir "$env:LOCALAPPDATA\Astrorder-Wails-Dev"
```

`30102` 是开发示例，骨架不会自动启动此服务。无 `--backend` 时业务 API 返回 503。只接受明确 loopback IP 的 HTTP origin；拒绝生产端口 `30001`、远程目标、userinfo/query/fragment 和业务路径。默认状态为 `%LOCALAPPDATA%/Astrorder-Wails-Dev`，拒绝 `%LOCALAPPDATA%/Astrorder` 及子目录。接手 AI 仍须检查别名/重解析点与完整隔离启动参数，不能只依赖这层字符串校验。

## 交付时验证

- 前端：101 个文件、430 项测试通过；类型检查和 Vite 构建通过，lint exit 0，但有现存警告。
- 桌面 Go：测试、vet、module verify、EXE 构建和 `--check` 通过。
- 真实 WebView2：`started:true`、`bindingReady:true`、`kernelManagement:true`；登录、正式上传函数及字节读回、多图/缩放/复制与权限隔离通过。图片保存对话框触发后，自动化未取得焦点，保存文件未验收；未点击完整聊天发送入口。退出日志仍有 `Chrome_WidgetWin_0` Error 1412。
- `contracts`：19 项测试通过，vet 通过；App/Daemon 命令包在 Go 1.27.2 下编译、module verify 通过。
- 未重新验收后端全量测试。历史 Windows 独立进程 breakaway/ACL 失败仍是发布阻塞，不能用上述命令包编译结果覆盖。

展示版本已按用户授权替换本机 Electron，用户确认启动和目标展示正常。后续目标管理与图标/托盘状态修复尚未应用生产；参见 [目标管理](../docs/reports/native-goal-projection.md) 和 [Windows 托盘修复](../docs/reports/wails-windows-tray-fix.md)。未 commit 或 push。以上早期验收记录保留其原始范围；最新完整构建中大小内核全量测试及 vet 已通过。
