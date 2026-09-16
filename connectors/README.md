# Astrorder 智能体连接器 (Embedded Connectors)

星序连接器（Connectors）负责星序控制面与底层各大原生 AI Agent 运行时的生命周期桥接与事件双工同步。星序严格遵循**原生真值权威原则**，不使用平庸的命令行爬虫或虚假 Mock 数据，真实反映各个 Agent 的连接能力。

---

## 1. 支持的智能体连接器矩阵

| 智能体类型 | 协议与连接方式 | 宿主位置 | 核心支持能力 |
|---|---|---|---|
| **Codex** | `codex app-server` 标准 JSON-RPC 协议 (stdio / IPC) | Session Daemon / 本机 | 多轮会话、中断控制 (`turn/interrupt`)、模型与思考深度切换、三档审批模式 (`ask`/`auto`/`full`)、CDP 图像附件上传 |
| **Hermes** | 官方插件包装器 (`hermes_agent.plugins`) + TUI-Gateway | Session Daemon / 本机 | 会话生命周期流、实时观察流、模型配置同步、会话销毁对齐 |
| **Grok** | 本地 CLI 进程与远端 Grok 运行时通道 | Session Daemon / 远程 | 深度推理流式输出 (`grok-3-deepthink`)、多轮交互、状态快照 |
| **SSH 远程环境** | 原生 SSH PTY 隧道 | 远端 Linux/Unix 服务器 | 远程 Agent 自动发现、远程工作区终端交互、远程 Docker 与开发集群代理 |

---

## 2. 连接器安全规范

1. **凭证双轨严格隔离**：
   - `ASTRORDER_BROWSER_SECRET`（浏览器访问凭据）与 `ASTRORDER_CONNECTOR_SECRET`（连接器内部通讯密钥）强行解耦，且连接器凭据绝不返回至前端网络层。
2. **零凭证落盘原则**：
   - SSH 连接只记录主机地址、端口、用户与私钥本地引用路径，系统内部绝不留存或持久化私钥文本、明文口令。
3. **安全沙箱受控接入**：
   - 所有连接器进程严格运行在隔离的子进程树下，大内核仅通过本地 Loopback IPC 与常驻 Session Daemon 守护进程通讯，防止主服务迭代导致连接断裂。

---

## 3. 本地连接安装与运行

### 3.1 Hermes 本地安装
```bash
# 安装 Hermes 连接器插件包
uv pip install ./connectors/hermes
```
在星序前端「连接管理」中点击连接本机 Hermes 时，星序会自动在当前活动的 Hermes profile 插件目录下配置受控轻量包装器并完成插件激活。

### 3.2 Codex 伴随进程运行
```bash
# 安装 Codex 连接器组件
uv pip install ./connectors/codex
astrorder-codex-connector
```
在 Session Daemon 模式下，守护进程小内核会自动托管 Codex `app-server` 子进程，支持多任务并发调度与无感断线重放。
