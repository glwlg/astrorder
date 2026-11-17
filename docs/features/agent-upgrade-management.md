# Agent 统一升级管理与版本维护指南

星序（Astrorder）提供了一站式、跨环境（本机 Windows / 本机 Linux / 远程 SSH 虚拟机）的 **Agent 原生版本统一升级管理能力**。开发者无需每天在不同系统的终端窗口里记忆和执行复杂的命令语法，直接在星序中一键触发、静默执行并在后台安全运行。

---

## 🛠️ 各 Agent 原生升级规范与协议映射

星序尊重各主流 Agent 的官方设计与生态工具链，后端接口 `/api/v1/agents/{agent_id}/upgrade` 会自动嗅探该 Agent 所属环境并路由至其对应的原生升级规范：

| Agent | 环境 | 自动调用的官方命令 | 执行与安全机制 |
| :--- | :--- | :--- | :--- |
| **Codex** | 本机 / SSH（含 WSL 连接） | `vp install -g @openai/codex@latest`，未找到 vp 时使用 npm | 根据目标环境查找包管理器，不因一次安装失败切换命令重试 |
| **Hermes** | 本机 / SSH（含 WSL 连接） | `hermes update --yes --no-gateway-restart` | 升级时不主动重启 Hermes gateway |
| **Grok** | 本机 / SSH（含 WSL 连接） | `grok update` | 调用目标上已有的原生可执行文件 |
| **Antigravity** | 本机 / SSH（含 WSL 连接） | `agy update` | 调用目标上已有的原生可执行文件 |

---

## 💡 核心交互与工程特性

### 1. 彻底静默隐藏系统终端（No Console Window）
在 Windows 平台下，星序利用 Windows 子进程静默标志（`CREATE_NO_WINDOW (0x08000000)` 与 `SW_HIDE`），彻底杜绝了传统调用脚本时弹出的黑色 `conhost.exe` / `cmd.exe` 黑框窗口，所有交互保持在星序精美沉浸的 UI 内完成。

### 2. 全实时流式输出（Real-time Streaming Output）
Go 后端升级接口采用 `application/x-ndjson` 协议，通过管道实时读取子进程的标准输出与标准错误流并即时推送前端。前端控制台以等宽终端字体逐行显示安装进度。

### 3. 挂起至后台任务中心（Background Task Integration）
- 点击「检查并升级」时，任务会自动登记在星序右上角的 **全局后台任务中心（Background Tasks）**；
- 如果在升级过程中随手关闭了日志弹窗，**升级进程不会被杀死**，仍然会在后台安全下载并执行；
- 用户可以继续在星序中对话或调度任务，随后可随时从后台任务中心点击「查看日志」重新拉起控制台监控流；
- 后台任务中心的取消操作调用服务端任务取消入口，由 Go 执行器取消对应升级进程。

当前实现位于 `app-server-go/internal/connections/upgrade.go` 与 `upgrade_jobs.go`，前端入口为 `frontend/src/features/agents/EnvironmentConnections.tsx`。版本探测与实际升级是不同操作；升级任务不是 Session Daemon 更新流程。
