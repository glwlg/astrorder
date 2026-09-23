# Agent 统一升级管理与版本维护指南

星序（Astrorder）提供了一站式、跨环境（本机 Windows / 本机 Linux / 远程 SSH 虚拟机）的 **Agent 原生版本统一升级管理能力**。开发者无需每天在不同系统的终端窗口里记忆和执行复杂的命令语法，直接在星序中一键触发、静默执行并在后台安全运行。

---

## 🛠️ 各 Agent 原生升级规范与协议映射

星序尊重各主流 Agent 的官方设计与生态工具链，后端接口 `/api/v1/agents/{agent_id}/upgrade` 会自动嗅探该 Agent 所属环境并路由至其对应的原生升级规范：

| Agent | 环境 | 自动调用的官方命令 | 执行与安全机制 |
| :--- | :--- | :--- | :--- |
| **OpenAI Codex** | 本机 (Windows) | `vp install -g @openai/codex@latest` | 优先通过 Vite+ 包管理器无感覆盖全局 CLI，静默隐藏 Windows 控制台弹窗 |
| **OpenAI Codex** | 远程 (Linux / WSL) | `vp install -g @openai/codex@latest \|\| npm install -g @openai/codex@latest` | 通过 SSH 安全通道执行，继承系统环境变量与 PATH |
| **Hermes Agent** | 本机 / 远程 (Linux / WSL) | `hermes update` | 原生触发 Hermes 自动拉取更新、更新模型目录、自动热重启 gateway 与 serve 守护进程 |
| **xAI Grok** | 本机 (Windows) | `powershell -NoProfile -Command "irm https://x.ai/cli/install.ps1 \| iex"` | 通过 PowerShell 静默拉取官方安装脚本并完成二进制覆盖安装 |
| **xAI Grok** | 本机 / 远程 (Linux / WSL) | `curl -fsSL https://x.ai/cli/install.sh \| bash` | 官方 Shell 脚本静默升级安装 |

---

## 💡 核心交互与工程特性

### 1. 彻底静默隐藏系统终端（No Console Window）
在 Windows 平台下，星序利用 Windows 子进程静默标志（`CREATE_NO_WINDOW (0x08000000)` 与 `SW_HIDE`），彻底杜绝了传统调用脚本时弹出的黑色 `conhost.exe` / `cmd.exe` 黑框窗口，所有交互保持在星序精美沉浸的 UI 内完成。

### 2. 全实时流式输出（Real-time Streaming Output）
后端升级接口采用 `StreamingResponse`（`application/x-ndjson` 协议），通过管道实时读取子进程的标准输出与标准错误流并即时推送前端。前端控制台以等宽终端字体逐行滚屏显示安装进度（包下载速度、编译状态、校验结果等），避免长时间无响应的“假死”体验。

### 3. 挂起至后台任务中心（Background Task Integration）
- 点击「检查并升级」时，任务会自动登记在星序右上角的 **全局后台任务中心（Background Tasks）**；
- 如果在升级过程中随手关闭了日志弹窗，**升级进程不会被杀死**，仍然会在后台安全下载并执行；
- 用户可以继续在星序中对话或调度任务，随后可随时从后台任务中心点击「查看日志」重新拉起控制台监控流；
- 只有在后台任务中心中明确点击 **「取消任务」** 红色操作时，星序才会发送 `SIGTERM` / `AbortSignal` 真正中止进程。
