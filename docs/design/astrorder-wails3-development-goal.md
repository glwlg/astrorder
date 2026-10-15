# 星序 Wails 3 开发 Goal

将下文完整交给开发 AI。该任务从已搭建的框架继续，不重新建项目。

---

你正在 Windows 星序仓库 `P:/workspace/glwlg/ai/astrorder` 中完成桌面壳从 Electron 到 Wails 3 的迁移。你的目标是交付功能完整、可实际使用、通过隔离安装验收的 Wails 桌面候选 EXE 安装器。必须编辑代码、运行构建并验证行为；不能只写方案、TODO、假实现或只读展示。

## 先读取这些内容

1. 仓库 `AGENTS.md`。
2. `desktop-wails/README.md`、`desktop-wails/toolchain.json` 与现有 Go 源码。
3. `docs/design/astrorder-wails3-migration.md`，以其中 W01–W16 和 A01–A12 为完整要求。
4. `desktop/main.js`、`desktop/preload.js`、`desktop/preview-preload.js`、`desktop/service-startup.js`、`desktop/stage-go-artifacts.js`，追踪原有桌面能力、调用者和进程生命周期。
5. `frontend/src/main.tsx`、`frontend/src/api/client.ts`，搜索所有 `astrorderDesktop`、`astrorderPreview`、附件 `source_path` 调用点。读取相关定义和全部调用链后再改。

先运行 `git status --short` 与 `git branch --show-current`。保留现有未提交修改及四张设计图片，不 checkout/stash/清空工作树。直接在现有工程继续。

## 已准备的工程与工具

- `desktop-wails/` 是独立 Go module，嵌入共享 `frontend/` 构建，不再创建另一套 React 页面。
- `.tools/wails/node-v26.11.1-win-x64/node.exe`、`.tools/wails/bin/wails3.exe` 已安装，Go 自动工具链为 `1.27.2`。
- Wails Go/CLI `v3.0.0-beta.28` 与 runtime `3.0.0-beta.28` 配套；前端直接依赖及 lock 已升级并精确锁定。
- `scripts/setup-desktop-wails.ps1` 可复现工具/锁依赖安装。统一入口 `scripts/desktop-wails.mjs` 支持 `install|bindings|build|check|test|run|smoke`。
- 当前 `/` 是开发绑定检查页，`/chat` 承载共享 React。`Info/Minimize/Maximize/IsMaximized` 是真实 Go 方法；smoke 已通过真实 WebView JS→Go 调用。
- 当前尚未接 `window.astrorderDesktop`、服务控制、鉴权、托盘、开机启动、通知、原生附件/剪贴板/预览、安装器。HTTP handler 有显式本机代理，WebView 中的业务 HTTP/WS/音频仍待真实验证。
- 骨架默认不启动或停止任何内核。默认 state 为 `%LOCALAPPDATA%/Astrorder-Wails-Dev`，生产 state 及端口 30001 被拒绝。示例开发 backend `http://127.0.0.1:30102` 不表示服务已启动。
- 前端当前 99 文件418测试通过，类型/构建通过；桌面 Go 测试/vet/build/smoke 通过。后端全量存在历史 Windows breakaway/ACL 失败，须重新定位、修复或明确阻塞；不要直接声明全绿。

## 不可改变的架构与边界

```text
Wails 桌面系统能力
      ↓ 共享 React 通过 HTTP / WebSocket
Go App Server（app-server-go/）
      ↓ 内部 gRPC
Go Session Daemon（session-daemon-go/）
      ↓ 托管真实 Agent / SSH / PTY
```

Wails bindings 只承接桌面能力，业务继续共用浏览器/手机 HTTP/WS。不得把会话运行时移进 Wails 进程、恢复旧 Python daemon，或把全部业务改成 Go bindings。Go 程序/配置缺失必须明确报错，不添加静默兜底，不伪造成功或 token 用量。

本任务只授权开发、构建、隔离运行和隔离安装验收；不授权 commit、push、生产部署、生产配置/数据库/聊天读取。不得访问 `P:/DevApp/Astrorder` 的生产数据或停止既有服务。新建隔离 state、端口与安装目录，清理时仅操作本任务创建且身份验证过的进程。

遵守 AGENTS.md：小内核停止/重启前读其权威状态；`running/waiting_approval` 或状态不可读时，必须取得明确同意。不能因为“使代码生效”而重启生产小内核。关闭窗口隐藏至托盘，大小内核继续；显式退出停止大内核后退出壳，小内核与托管进程继续；分别验证各条路径。

保持 native Agent 名称 codex；星序只调度。不要重装用户已卸载的 Codex OpenViking 插件。不得在日志、URL、报告、截图或安装资产中输出凭据。

## 按下面顺序完成，逐步跑通

### 第一步：完成真实业务贯通

复现 `build/check/test/smoke`，修复实际错误。读取 ctl 的真实参数与隔离配置契约，提供可重复执行的隔离开发启动/清理入口，先启动 daemon 并确认 readiness，再启动 App 并确认健康。所有相关路径和端口显式指定，不能复用生产默认值。

验证 Wails origin/runtime/Go binding 的真实支持方式。建立集中桌面适配层，保持普通浏览器入口可用；同源代理或 API base 必须覆盖登录、cookie、API、事件 WS、终端 WS、音频 WS、附件和原始文件。检查 Wails asset server 对 HTTP method/WS upgrade 的实际处理，不能只靠标准 ReverseProxy 单测推断可用。外链与不可信内容不获得原生特权；禁止关闭 WebView 安全策略、全域 CORS 或 URL token。

在真实 Wails WebView 完成：隔离登录 → 新建受控测试会话 → 发送一次真实 Agent 请求 → 观察流式输出和完成状态 → 读回历史 → 刷新/重连。不得用伪回复证明可用，不得重复发送结果不明的命令。把开发检查页改成正式 splash/状态入口，并保留独立 smoke 入口。

### 第二步：补齐全部桌面能力

以 W01–W16 为清单逐项追踪旧实现，更新 frontend bridge 类型和全部使用点，接入实际 UI。完成窗口/托盘/单实例、服务状态与操作、开机启动、通知、可信原生文件选择/拖放、文件/图片剪贴板、图片预览/保存、外链导航与快捷键、麦克风和本地流式听写。

不要把同步 `getPathForFile(File)` 直接替换成 Promise 后漏改调用者；统一异步链路，并由可信原生选择/拖放取得路径。禁止用文件名拼绝对路径。分别验证字节、名称、真实路径及 session/connection 上下文；浏览器仅字节上传仍合法。

为预览窗口提供受限服务门面；不能因前端隐藏按钮就认为无服务控制权限。中文 IME 合成期间不能误发送，终端 Ctrl+C/粘贴、Monaco、多屏 DPI、恢复焦点、听写取消和长录音必须真实验收。未实现能力明确报错，禁止空返回值或无操作假成功。

### 第三步：完成 Windows 候选安装器与隔离验收

建立 Wails 正式构建和独立安装链，不继续依赖 Electron Builder。承接现有 stage 清单的全部 Windows 程序、Linux amd64/arm64 helpers、插件资产、图标与前端；核对实际清单，不凭数量猜文件。Go 正式构建使用 `-trimpath`，重新计算 manifest、SHA256，证明安装资产与本次构建逐字节一致。

实现 WebView2 检测/安装或可操作的缺失错误；开发工具和测试数据不进入安装器。只保留用户要求的 EXE 安装器，不交付额外 ZIP。不要假定 NSIS 已安装，查询真实官方最新兼容版本、安装并验证命令。

在独立安装目录测试全新安装、覆盖升级、卸载/重装和回滚，保留隔离数据及用户设置；验证关闭隐藏、显式退出、重开、App 独立更新及壳崩溃时 daemon 和 Agent/SSH/PTY 身份与输出连续。不能仅比较持久 daemon ID，应比较 PID、创建时间和验证过的 executable。

运行受影响 focused checks，再执行最终前端/Go/旧桌面行为基线与资产验证；对新错误修根因。现存 Windows lifecycle/ACL 错误不能通过 skip、删测试或放宽断言掩盖。

### 第四步：交付，停在生产切换之前

创建 `docs/reports/astrorder-wails3-acceptance.md`，逐项记录 W01–W16/A01–A12 的状态、实际命令、exit code、环境、行为证据和遗留问题。区分实现、隔离、安装及生产验收；A10 生产切换和依赖生产切换的最终清退标为等待授权，不能勾选。

候选验收前保留旧 Electron 作为行为参考；移除候选包对它的运行依赖。生产替换和活动 Electron 实现最终清退留待单独授权，不因本 goal 擅自执行 P4。

交付真实安装 EXE 的路径、SHA256、构建入口、启动方式和验收结果。更新 README，明确实际完成与未验证项。除非存在无法自行解决的依赖/环境/权限阻塞，持续推进到隔离候选安装验收完成；遇到阻塞先完成独立工作，再准确报告具体原因，不编造通过结果。

## 常用验证入口

仓库根目录 PowerShell：

```powershell
$Node = '.\.tools\wails\node-v26.11.1-win-x64\node.exe'
& $Node scripts/desktop-wails.mjs build
& $Node scripts/desktop-wails.mjs check
& $Node scripts/desktop-wails.mjs test
& $Node scripts/desktop-wails.mjs smoke
```

框架/方法变更后重新生成 bindings，禁止手工修改生成文件。开发全程使用锁定工具；新增依赖先查询官方当前版本、检查兼容性并锁定精确版本，不默认旧版，也不能用未经查询的“最新版”标签。构建通过只证明构建，完成的标准是上述实际行为和可安装制品。
