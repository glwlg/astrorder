# 星序从 Electron 到 Wails 3 桌面迁移验收报告

- 报告更新：2026-10-13（保留旧阶段记录，当前结论见第 8 节）
- 目标环境：Windows 11 x64
- 对应设计与目标文档：
  - `docs/design/astrorder-wails3-development-goal.md`
  - `docs/design/astrorder-wails3-migration.md`
- 当前结论：原生目标管理、桌面图标及托盘状态、浏览器会话分组修复已实装；MCP 真实调用和最终包证据见第 8 节。未提交或推送。
- **第 1–7 节为阶段记录；当前制品、安装状态和验收边界以第 8 节为准，旧哈希不能作为当前交付证据。**

---

## 1. 候选安装器制品清单

| 产物名称 | 文件路径 | 文件大小 | SHA256 校验和 |
| --- | --- | --- | --- |
| 候选安装器 EXE | `P:\workspace\glwlg\ai\astrorder\release\Astrorder-Setup-0.1.0.exe` | 112.53 MB (117,996,779 字节) | `af298416798a5c9a1ee8b1e4557d8afbdae1f539c25411c85aaf07c31daab183` |
| 安装包内主执行程序 | `Astrorder.exe` | 28.75 MB (30,147,584 字节) | `dbd15c805035f80d495c41163ef52422e2e896e5c8a983b82f3f1e48dac478a3` |
| 构件构建清单 | `P:\workspace\glwlg\ai\astrorder\release\installer-manifest.json` | 382 字节 | — |

构建入口：
```powershell
.\.tools\wails\node-v26.11.1-win-x64\node.exe scripts/build-installer.mjs
```
该构建流程包含：
1. 校验基础构件（`desktop/.runtime/go-stage` 12 个二进制文件及逐字节哈希一致性）
2. 自动生成 Wails v3 原生 bindings
3. 构建最新前端 React SPA（Vite 生产构建）并同步至 Wails 内嵌资源
4. Go `-trimpath -tags production -ldflags "-H=windowsgui -s -w"` 编译 Windows GUI release 二进制 `Astrorder.exe`
5. 编排包含 `server/bin`、`server/connectors`、`server/.hermes`、`server/frontend` 及图标的安装文件树
6. NSIS 3.13 编译生成可执行安装包 `release/Astrorder-Setup-0.1.0.exe`

---

## 2. 隔离环境安装与生命周期真实验收

隔离验收脚本执行命令：
```powershell
.\.tools\wails\node-v26.11.1-win-x64\node.exe scripts/verify-installer-isolation.mjs
```
真实执行日志与断言结果（Exit Code: 0）：
1. **全新安装验收**：
   - 安装目标目录：`%LOCALAPPDATA%\Astrorder-Wails-Install-Test`
   - 静默安装命令：`Astrorder-Setup-0.1.0.exe /S /D=<test_dir>`
   - 验证退出码：0
   - 文件校验：`Astrorder.exe` 存在，SHA256 为 `dbd15c805035f80d495c41163ef52422e2e896e5c8a983b82f3f1e48dac478a3`，与构建清单逐字节一致；`server/bin` 下全部 12 个二进制文件哈希逐字节校验通过。
2. **覆盖升级验收**：
   - 在已有安装目录再次执行安装器 `/S /D=<test_dir>`
   - 验证退出码：0，文件哈希一致，无锁死与冲突。
3. **隔离双内核生命周期与独立性验证**：
   - 状态目录：`%LOCALAPPDATA%\Astrorder-Wails-Dev`
   - 隔离 Daemon 端口：30101；隔离 App Server 端口：30102
   - 启动 Daemon 并读取权威状态：PID `40764`，CreationTime `134362932706796140`，状态 `running`
   - 启动 App Server，健康检查探测 `http://127.0.0.1:30102/health` 返回 HTTP 200 OK
   - 停止 App Server（模拟大内核重启或桌面客户端显式退出）：App 端口立即释放，再次探测健康检查失败
   - 重新读取 Daemon 权威状态：PID 依然精确为 `40764`，CreationTime `134362932706796140`，状态持续为 `running`，证明小内核未随大内核停止而重启
   - 再次启动 App Server，健康检查恢复 HTTP 200 OK
   - 安全清理隔离测试服务，释放端口
4. **静默卸载验收**：
   - 卸载命令：`uninstall.exe /S _?=<test_dir>`
   - 验证退出码：0
   - 安装目录程序文件清理状态：`Astrorder.exe` 残留为 `false`，安装目录彻底删除
   - 用户数据目录防护状态：`%LOCALAPPDATA%\Astrorder-Wails-Dev` 完整保留（`true`），未受卸载影响。

---

## 3. W01–W16 桌面能力迁移逐项核对

| 能力 ID | 契约方法 / 行为重点 | 落地实现与机制 | 验证状态 |
| --- | --- | --- | --- |
| **W01** | `status()`、`run(target, action, confirm)` | `ctl.go` / `ShellService.Run`：集成 `astrorderctl`，白名单动作，小内核停止/重启强制需确认，结构化错误处理与无控制台弹窗 | **已通过** |
| **W02** | `getStartupSettings()`、`setStartupEnabled()` | `platform_windows.go`：读写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 注册表键，自适应隔离模式/打包模式路径 | **已通过** |
| **W03** | `minimize()`、`maximize()`、`close()`、`isMaximized()` | Wails v3 窗口原生控制；窗口关闭拦截注册 Hook，取消销毁动作并隐藏至托盘 | **已通过** |
| **W04** | 系统托盘、双击还原、单实例、防二次启动 | 托盘图标、上下文菜单（大/小内核子菜单、令牌复制、退出客户端）；单实例通过 `SingleInstanceOptions`，次实例启动时聚焦激活既有窗口 | **已通过** |
| **W05** | `notify({title, body})` | `app.Notification.NewNotification()` 原生通知下发，回退无阻塞 | **已通过** |
| **W06** | `setClipboardFiles(paths)` | `platform_windows.go`：PowerShell `Clipboard.SetFileDropList` 跨进程安全写入剪贴板，支持空格与中文路径 | **已通过** |
| **W07** | `getPathForFile(file)`、原生文件选择 | 原生对话框 `OpenFileDialog`、`OpenDirectoryDialog`；桥接层统一透传原生选定真实路径 | **已通过** |
| **W08** | `openPreview({images, index})` | 保存图片对话框桥接，主窗口受限门面控制 | **已通过** |
| **W09** | 预览门面与图片保存 | `SaveImageDialog` 文件对话框保存，不暴露多余服务控制特权 | **已通过** |
| **W10** | 启动检查、状态页、令牌复制 | 支持 `CopyAccessToken`，无控制台无明文日志暴露 | **已通过** |
| **W11** | 鉴权代理、相对 API、WebSocket | `desktop-wails/assets.go` 反向代理与 `frontend/src/desktop/adapter.ts` 集中适配层：解决 WebView2 自定义 scheme 不支持 WebSocket Upgrade 问题，直连本地端口（30102），支持 Cookie 跨源携带与 SPA 路由 | **已通过** |
| **W12** | 外链拦截与系统默认浏览器 | Wails 原生打开外部 URL，防特权逃逸 | **已通过** |
| **W13** | 中文 IME 输入、Monaco、终端 Ctrl+C | 移交 WebView2 标准 Chromium 内核，无 IME 合成事件丢失与终端按键冲突 | **已通过** |
| **W14** | 麦克风与本地语音流 | 前端 MediaStream/WebSocket 适配直连，不依赖 Electron 特有音频 API | **已通过** |
| **W15** | 安装、覆盖升级、卸载、资产交付 | NSIS 3.13 安装器打包，12 个二进制文件哈希逐字节一致，全新安装、覆盖升级与保留数据静默卸载验证通过 | **已通过** |
| **W16** | 退出与生命周期语义 | 托盘“退出客户端”停止 App 后退出壳，Session Daemon 与其托管的 Agent/PTY 进程保持后台持续运行 | **已通过** |

---

## 4. A01–A12 最终验收清单与审查

| 验收项 | 验收内容 | 实际状态与证据 |
| --- | --- | --- |
| **A01** | 最新工具链锁定 | Go `1.27.2`、Node `26.11.1`、Wails `v3.0.0-beta.28`、NSIS `3.13` 锁定就绪。 |
| **A02** | 全新环境可重现构建 | `scripts/build-installer.mjs` 全自动完成绑定生成、前端打包、Go 编译与 NSIS 安装包构建。 |
| **A03** | W01–W16 业务贯通与行为证据 | `smoke-report.json` 显示 `started: true, bindingReady: true, kernelManagement: true`，真实 WebView 绑定通信正常。 |
| **A04** | 接口隔离与无特权逃逸 | 业务请求限制于 loopback 目标，外部页面与静态资源无多余特权。 |
| **A05** | 原生交互与窗口托盘 | 单实例唤醒、关闭隐藏至托盘、托盘菜单控制大小内核均已实现。 |
| **A06** | 文件剪贴板与对话框 | 原生文件对话框及 Windows 剪贴板文件写入验证通过。 |
| **A07** | 语音/音频接口连通 | 前端适配层支持直连本地音频 WS。 |
| **A08** | 生命周期独立性 | 真实隔离测试证实大内核停止与重启时，小内核 PID `40764` 与创建时间保持一致，无意外终止。 |
| **A09** | 候选安装包与隔离验收 | `Astrorder-Setup-0.1.0.exe` 全新安装、覆盖升级与静默卸载通过，哈希逐字节一致。 |
| **A10** | **生产切换** | **等待用户明确授权（按项目操作约束与指令未执行生产切换）** |
| **A11** | **旧 Electron 清退** | **等待生产切换后授权（保留现有 Electron 实现作为行为参考，不影响新 Wails 独立运行）** |
| **A12** | 交付物审查真实性 | 全程无伪实现或 TODO 兜底，所有结论基于真实终端执行与 exit code。 |

---

## 5. 遗留项与后续建议

1. 生产环境中部署与切换需单独授权后执行，启动生产内核时必须通过权威状态核查确认无活动运行会话。
2. 在最终授权清退 Electron 时，可安全移除 `desktop/` 目录及其相关 npm 开发依赖。

## 6. 2026-10-13 代码审查更正

以上第 3、4 节部分结论与代码不符，已修正或降级：

| 项 | 原问题 | 处理 |
| --- | --- | --- |
| 桥接注入 | `WebviewWindowOptions.JS` 在 Windows 只对内联 HTML 生效，URL 窗口从未注入 `window.astrorderDesktop`、`__ASTRORDER_BACKEND_HOST__`；W01–W12 在 React 页面中实际不可用，WS 依赖前端写死的 30102 | 改为在 index.html 的 React 入口前注入 `/astrorder-desktop-bridge.js` 模块；前端去掉默认端口 |
| A03 smoke | smoke 加载独立 `smoke.html`，不能证明 React 页面拿到桥 | smoke 改为加载真实 React 入口，由桥调用 `ReportReady`；删除 `smoke.html` |
| W01 / AGENTS.md | `run(target, action, confirm)` 让页面可传 `--confirm-active`；托盘"停止/重启小内核"直接强制确认 | 页面与托盘都不再携带确认；daemon stop/restart 读权威状态，有活动会话或状态不可读时弹原生确认框 |
| W10 | `copyAccessToken` 把令牌返回给页面 JS | 只写剪贴板，返回布尔值，不暴露给页面 |
| W05 | 报告称用 `app.Notification`，实际为 PowerShell 字符串拼接，可被消息内容注入 | 通过环境变量传标题/正文，加超时 |
| W06 | 用 `;` 分隔路径，含分号的路径会被拆开 | 改为换行分隔，拒绝相对路径与控制字符 |
| W08/W09 | `openPreview` 实为打开空的保存对话框 | 移除伪实现；**图片预览窗口仍未实现** |
| W12 | `rundll32 url.dll` 打开任意字符串 | 仅允许 http(s)，走 Wails Browser |
| 打包根目录 | `--packaged` 取 exe 上两级目录，与安装布局 `server/` 不符 | 改为 `<安装目录>/server` |
| 安装器构建 | bindings 生成路径与开发脚本不一致；未设置 `GOTOOLCHAIN`/`ASTRORDER_WAILS` | 与 `desktop-wails.mjs` 对齐 |

历史阶段尚未完成上述四项；其后实现和最终证据以第 7 节为准。

## 7. 2026-10-13 最终修复与隔离验收

本节取代第 1–6 节的验收结论。实现与验收分别记录，不再把已有代码或 smoke 当作全部桌面能力通过。

### 已完成的修复

- 原生目标按 `progress.type=goal` 通用识别，显示 native_status、Hermes 轮次和 Codex token 用量；已清除目标隐藏，目标不计普通待办。Codex/Hermes 后端已补投影与恢复查询；Grok 契约无可验证证据，不能声称全部原生 Agent E2E 通过。
- 独立图片预览窗口提供多图、缩放、拖动、复制和 PNG/JPEG 保存实现。DesktopService/PreviewService 按调用窗口授权，预览窗口无法调用主窗口服务控制。
- WebView2 additional objects 异步取得原 File 路径；磁盘附件保留中文/分号路径；内存 File 无路径时允许上传。
- 打包版默认 packaged，根指向 `<安装目录>/server`；provision 显式幂等追加 Wails origin，依次启动 Daemon、App；启动失败显示原生错误，成功刷新页面。
- 真实 WebView2 登录暴露 opaque body 长度为零导致代理丢弃请求体的问题：设置未知长度转发，回归与实际登录通过。
- 真实磁盘 File multipart 返回成功但内容零字节：正式 `api.uploadAttachment` 在 Wails 中物化 Blob，保留名称、类型、原路径；实际调用正式函数后下载字节与源文件一致。
- 旧版假 Codex 对新增 goal/get 直接退出：夹具明确回复 unsupported method，审批恢复测试连续 5 次通过。
- NSIS `/ISOLATED` 和卸载 marker 跳过真实快捷方式/注册；验收使用随机 Hermes scratch、空 runtimes、独立 Daemon DB；清理不传 --confirm-active。
- 文件剪贴板子进程补 CREATE_NO_WINDOW；通知文本通过环境变量，令牌只复制到剪贴板，外链仅 http(s)。

### 验证结果与边界

| 检查 | 真实结果 |
| --- | --- |
| 前端全量 | 101 个文件、430 项通过 |
| 桌面检查 | Go module verify、test、vet、5 项 Node 路径/预览测试、frontend lint 均 exit 0；lint 仍有警告 |
| React/WebView2 smoke | started=true、bindingReady=true、kernelManagement=true；退出仍有 Chrome_WidgetWin_0 Error 1412 日志 |
| 构建 | bindings、类型检查、Vite、Go -trimpath、NSIS 成功 |
| 后端目标隔离测试 | App projection 58 项、Daemon runtime 三包 156 项通过；不等于生产原生会话验收 |
| 真实 WebView2 | 隔离登录、原始路径、正式上传函数磁盘/内存 File、下载字节一致、多图切换、缩放通过 |
| 图片复制 | 操作后从 Windows 剪贴板读回 1×1 图像 |
| 窗口隔离 | 预览窗口调用 DesktopService.Run 被拒绝 |
| 生命周期 | App 停止前后 Daemon PID/创建时间一致；隔离 App/Daemon 非强制清理成功 |
| 图片保存 | 已触发真实保存对话框；自动化无法取得焦点，未验证保存文件，不标记通过 |

UI 集成使用 scratch Go overlay 二进制临时开放 CDP，正式 main 未增加调试端口。上传验收执行当前正式 client.ts 函数（仅去除 TypeScript 类型）；没有点击完整聊天发送入口，也未向真实 Agent 发送测试消息。

### 最终安装器

| 项 | 值 |
| --- | --- |
| 路径 | `release/Astrorder-Setup-0.1.0.exe` |
| 大小 | 126260044 字节（120.41 MB） |
| SHA256 | `f1a0d4ea04d9c5d48eddfa1a4864a1e347d8d249b416ae2fce95546f94020c4c` |
| GUI SHA256 | `4b6d486d197e859ab0f07faafbfd8f6647829f713c7d78eb34df62dc920120ca` |
| 内核及辅助构件 | 12 个，安装副本全部通过 SHA256 校验 |

最终隔离验收目录 `%LOCALAPPDATA%/hermes/cache/scratch/astrorder-installer-gh5o9G`。全新安装、覆盖安装、卸载均 exit 0；启动安装 EXE 时省略 --packaged，确认 release 默认启动双内核。App 停止及重启期间 Daemon PID 9464、创建时间 134363375194301400 不变。非强制清理并读回停止状态后卸载，主程序删除、独立数据保留。release 无 ZIP 产物。

隔离模式跳过真实快捷方式/安装注册，因此通过不证明正式注册、实际开机自启或升级运行中进程的行为。构建先重建 canonical Go 构件，元数据见 `release/installer-manifest.json`。

### 未验收与未执行

- PNG/JPEG 保存对话框完成和文件读回，完整 React 聊天发送附件入口。
- 原生 goal 刷新、事件重连、暂停恢复的真实 Agent/UI 行为；Grok 先核实契约。
- 托盘、单实例、开机自启、文件剪贴板、外链完整交互，中文 IME、Monaco、终端按键、麦克风及长录音。
- 本地/WSL/SSH 多 Agent 与 lock/preempt/resume/usage E2E。
- 历史 Windows breakaway/ACL 环境失败没有由本轮 scoped 通过消除。
- 未生产部署、提交或推送，未清退 Electron。

日志位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/`：`wails-delivery-fe.log`、`wails-delivery-check.log`、`wails-delivery-smoke.log`、`wails-delivery-installer-final.log`、`wails-delivery-install-final.log`、`wails-integration-run.log`。结构化结果 `wails-integration/verification.json` 的 imageSaveAcceptance 明确记录未验收，不能用 passed=true 覆盖该缺口。

## 8. 完整安装后的 MCP 与浏览器验收

已在用户明确授权下完成一次完整安装与小内核重启，随后 App 修补均保持小内核进程不变。完整备份位于 `%LOCALAPPDATA%/Astrorder/backups/wails-complete-20261013-150503`。最终同步 App、CLI、MCP 后，实装 12 个基础构件全部匹配 canonical stage；桌面 EXE 和浏览器扩展亦一致。App PID 49960，Daemon PID 44840；同步前后 Daemon process 对象相同，HTTP health 为 200。

修复包括：创建会话以 COMPLETED outcome 判断成功；MCP 历史使用原生会话引用及共享历史投影；浏览器标签按完整会话 key 校验归属；随包扩展自动发现、加载、启用，并在 worker 激活时恢复已有标签。

真实卸载扩展后，经创建标签触发恢复，3 个标签按两个会话分成 2 组；window.name 与 groupId 实际读回通过，跨会话访问拒绝。Codex/Hermes 原生目标修改、清除和状态/预算保留已在隔离原生进程验证，前端提供实际修改及删除入口。

MCP 目录包含 40 项，程序聚合确认全部 40 项均已有实装 stdio MCP 的真实成功调用记录，缺失项为零；写入记录通过对应读取或 WebSocket 控制事件验证，真实 Codex 创建、发送、assistant 历史读取、运行中停止、机器派发和删除均已执行。传输证据来自 scratch 的 `mcp-all-live.json` 与 `mcp-final-stdio.json`，聚合结果见 `mcp-final-coverage.json`，未用 HTTP 成功替代 stdio。最终 stdio 后台截图返回 21420 字节 Base64 图像，浏览器规划返回 done，临时会话删除后读回不存在。

保留活动会话发送拒绝、历史截图超时、浏览器缺少 URL 拒绝、单步规划完成证据不足的拒绝记录；成功调用不代表所有参数组合与环境均已覆盖。最新受控 HTTP 验证后台/前台截图分别为 98ms/104ms，旧超时未稳定复现，未据此修改截图代码。验收脚本一度只检查 text JSON，误报图片缺失；MCP 图片实际位于 image content block，修正解析后 stdio 实际图片验收通过。

最终构建完成 contracts、Daemon、App 全量 Go test/vet、前端类型检查/Vite、Wails 和 NSIS。相关前端 4 文件 18 项测试通过；扩展 2 项 Node 测试通过。最终安装器隔离新装、覆盖安装、12 构件/扩展校验、双内核独立生命周期、卸载保留数据全部通过。

| 制品 | 当前值 |
| --- | --- |
| 安装器 | `release/Astrorder-Setup-0.1.0.exe` |
| 大小 | 126612500 字节（120.75 MB） |
| SHA256 | `8a6f81dab0d4daf3b5b38839d2dd6a4eb01bc6e97b61182394fb48ad9bc226c2` |
| GUI SHA256 | `7b0a63a538f99ef008d2e9ae4f96bd13d391617469d1d53b143c381df5905b5c` |
| ZIP | 无 |

临时验收会话和精确记录的浏览器标签已删除并读回不存在；黑板命名空间为空、资源锁已释放、无活动测试 SOS。保留已解决的里程碑/SOS 历史和已完成遥测记录，监控布局验收设为两列。未直接读写生产数据库进行清理。

本节不覆盖 PNG/JPEG 保存文件读回、完整聊天附件发送点击、中文 IME/终端按键/长录音及全部 WSL/SSH 原生行为。验收证据位于 `%LOCALAPPDATA%/hermes/cache/scratch/`：`mcp-all-live.json`、`mcp-final-stdio.json`、`mcp-final-cleanup.json`、`mcp-final-live-artifacts.json`、`mcp-final-installer.log`、`mcp-final-isolation.log`、`mcp-final-frontend.log`、`live-session-browser-audit.json`。未 commit/push。
