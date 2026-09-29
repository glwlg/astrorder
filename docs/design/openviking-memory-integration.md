# Astrorder OpenViking 记忆与知识库集成规范 (Agent 直连 + 星序下发与展示)

> **状态**：Approved / Ready for Development  
> **所属版本**：Astrorder v0.1.0+ Architecture  
> **核心原则**：
> 1. **Agent 直连 OpenViking（Direct Runtime Connection）**：Codex 通过官方 Hook + MCP、Hermes 通过内置 Tools 直连 OpenViking 服务，闭环完成记忆自动 Recall、增量捕获与提炼，星序绝不介入执行链路。
> 2. **星序职责专注两件事**：
>    - **控制面（Control Plane）**：自动化配置配发与环境一致性治理（类似 Model Sync，负责跨 Windows/WSL/Debian 下发 `ovcli.conf`、激活 Hook/MCP、健康检测）。
>    - **观察面（Observation Plane）**：侧边栏（Sidecar）只读展示 `viking://` 虚拟文件系统、L0/L1/L2 阶梯视图与全局语义检索。
> 3. **许可证与进程绝对隔离（License & Process Safety）**：OpenViking 作为独立服务运行，星序后端无源码级侵入，通过标准 HTTP 只读 API 读取看板数据。

---

## 1. 架构定位与思考边界（Critical Thinking & Boundary）

在经过充分推敲与方案对比后，我们确立了**“执行链路走原生直连，控制与观察由星序赋能”**的全新原则：

```text
 ┌────────────────────────────────────────────────────────────────────────┐
 │                        Astrorder 桌面端 / 控制台                        │
 │                                                                        │
 │   ┌───────────────────────────────┐  ┌──────────────────────────────┐  │
 │   │  [控制面] 网关与配置同步中心   │  │ [观察面] Sidecar 知识记忆看板│  │
 │   │  (OpenViking Provisioning)    │  │ (viking:// 树状浏览 / 搜索)  │  │
 │   └───────────────┬───────────────┘  └──────────────▲───────────────┘  │
 └───────────────────┼─────────────────────────────────┼──────────────────┘
                     │ 自动化配置下发                  │ 纯只读 HTTP 查询
                     │ (Local / WSL / SSH)             │ (L0/L1/L2 预览)
                     ▼                                 │
 ┌─────────────────────────────────────────────────┐   │
 │         多环境 Agent 运行时 (Local / Remote)    │   │
 │                                                 │   │
 │   ┌───────────────────┐   ┌─────────────────┐   │   │
 │   │ Codex CLI         │   │ Hermes TUI/CLI  │   │   │
 │   │ • UserPromptSubmit│   │ • Built-in      │   │   │
 │   │ • Stop / PreCompact│  │   viking_*      │   │   │
 │   │ • Stdio MCP Proxy │   │   tools         │   │   │
 │   └─────────┬─────────┘   └────────┬────────┘   │   │
 └─────────────┼──────────────────────┼────────────┘   │
               │                      │                │
               │ (Agent 直连执行闭环)  │                │
               ▼                      ▼                │
       ┌───────────────────────────────────────┐       │
       │           OpenViking Server           │───────┘
       │           (Context Database)          │
       │                                       │
       │ • 虚拟文件系统 (viking://)            │
       │ • 语义检索与向量索引                  │
       │ • 会话总结与长短期记忆提取            │
       └───────────────────────────────────────┘
```

### 1.1 为什么星序不做中间代理（Proxy / Interceptor）？
1. **零破坏 Agent 原生体验**：
   - Codex 官方推出的 `codex-memory-plugin` 是基于生命周期 Hooks（`UserPromptSubmit`, `Stop`, `PreCompact`）和 Stdio MCP 实现的，它能极其精准地在 Prompt 提交前注入相关记忆、在回合结束时增量收集上下文、在 Context Compaction（压缩）前强制提炼记忆。
   - Hermes 本身拥有官方适配的内置工具集（`viking_search`, `viking_read`, `viking_remember` 等）。
2. **免除中间层失真**：如果在星序中间加一层代理做所谓的记忆拼接，会导致流式响应（Streaming）难以处理、Token 统计与消耗归属模糊、网络中断排障复杂度成倍上升。
3. **职责清晰单一**：Agent 专心做思考与工具调用，星序专心做**“跨环境运维管理”**与**“可视化看板”**。

---

## 2. 星序控制面：自动化配置下发与治理（Provisioning）

如同星序在 `core/model_sync.py` 中实现了对 Grok/Codex 模型配置跨本地、WSL 与 Debian SSH 的一键比对与下发，星序将新增 **OpenViking Provisioning 模块**，彻底解决多端配置漂移与手动配发的痛点。

### 2.1 需下发的配置文件与目标规范

无论在哪个目标环境（Local Windows / WSL / 远端 Debian），Agent 直连 OpenViking 所需的核心配置包含两部分：

#### A. OpenViking CLI 核心连接配置 (`~/.openviking/ovcli.conf`)
```json
{
  "url": "http://192.168.1.100:1933",
  "api_key": "[REDACTED]",
  "plugin": {
    "autoRecall": true,
    "recallLimit": 5,
    "recallQueryExpansion": "off",
    "recallCompress": "off",
    "codex": {
      "autoCapture": true
    }
  }
}
```

#### B. Codex 插件与 Hooks 启用配置 (`~/.codex/config.toml`)
确保各环境的 Codex 开启了插件与 Hooks 功能：
```toml
[features]
plugin_hooks = true
```

#### C. Codex 插件安装与 marketplace 自动化安装状态
检测目标环境中是否存在官方插件：`openviking-memory@openviking`。星序通过 headless shell 执行官方幂等脚本或 `codex plugin add` 完成静默安装。

### 2.2 Provisioner 服务设计 (`backend/src/astrorder/core/ov_sync.py`)

```python
from dataclasses import dataclass
from typing import Literal

@dataclass
class OvSyncTargetStatus:
    target_id: str                      # 'local', 'debian-dev', 'wsl'
    ovcli_config_status: Literal["synced", "outdated", "missing"]
    codex_hooks_status: Literal["enabled", "disabled", "unknown"]
    plugin_installed: bool
    server_reachable: bool
    latency_ms: float | None
    error: str | None = None

class OpenVikingSyncService:
    """管理 OpenViking 端点配置向各 Agent 环境（Local / WSL / SSH）的下发与校验"""

    def inspect_all_targets(self) -> list[OvSyncTargetStatus]:
        """巡检所有目标主机的配置与网络连通性"""
        ...

    def apply_config_to_target(self, target_id: str) -> bool:
        """
        向指定主机安全下发配置：
        1. 写入/更新 ~/.openviking/ovcli.conf (带权限控制 0600)
        2. 校验并修补 ~/.codex/config.toml ([features] plugin_hooks = true)
        3. 如缺失插件，通过 headless 脚本触发官方 marketplace 安装
        """
        ...
```

### 2.3 设置中心 UI（前端展示与一键下发）
在星序前端系统的 **设置 -> 服务与网关**（`UsageGatewaySettingsCard` 旁边）增加 **OpenViking 记忆网关** 面板：
- **服务器配置**：填入 OpenViking 服务端 URL 与 API Key（支持测试连通性）。
- **环境状态一览表**：
  - 本机 Windows：显示 `ovcli.conf` 状态、Codex Hooks 开关、连通延迟；
  - 远端 Debian：显示同步状态、一键下发按钮；
  - WSL 环境：显示同步状态、一键下发按钮。
- **一键同步全部环境**：点击后全自动将配置推送到所有受管环境，并平滑验证。

---

## 3. 星序观察面：Sidecar 知识记忆看板（Observation）

当各 Agent（Codex/Hermes）在底层与 OpenViking 互动时，用户在星序桌面端需要能够**随时查看项目沉淀了什么、架构文档是什么、搜索特定记忆**。

### 3.1 路由与只读 API (`backend/src/astrorder/routers/memory.py`)

星序 App Server 仅作为**只读客户端**直连 OpenViking 服务端口（默认 `:1933`），向前端提供轻量看板接口：

| 方法 | 路径 | 功能说明 |
| :--- | :--- | :--- |
| `GET` | `/api/v1/memory/health` | 检查 OpenViking 服务是否在线 |
| `GET` | `/api/v1/memory/tree?uri=...` | 获取 `viking://` 虚拟文件目录树节点（按项目/全局过滤） |
| `GET` | `/api/v1/memory/read?uri=...&level=...` | 读取条目内容（支持 `abstract`、`overview`、`full` 三种阶梯粒度） |
| `POST` | `/api/v1/memory/search` | 语义搜索项目或全局相关记忆，返回带 URI 和匹配度的结果列表 |

### 3.2 Sidecar UI 结构与交互形态

在星序右侧 Sidecar 工具栏中新增 **`知识记忆`** 选项卡（图标采用 `IconBrain` 或 `IconBookmarks`）：

```text
Sidecar 选项卡: [文件树] [变更] [终端] [图表] [知识记忆 🧠]
```

#### 视图组成：
1. **顶部搜索栏**：
   - 支持语义搜索输入框；
   - 筛选标签：`当前项目` / `全局偏好` / `架构规范`。
2. **中间树状资源导航（Tree Explorer）**：
   - 采用现有文件树成熟组件（复用展开收起、高亮、加载状态）；
   - 树节点展示：
     ```text
     📂 viking:// (OpenViking 知识库)
     ├── 📁 projects/astrorder/          [当前项目记忆]
     │   ├── 📄 decisions.md            (技术决策记录)
     │   ├── 📄 pitfalls.md             (踩坑与防沉降)
     │   └── 📁 specs/                  (规格说明书)
     ├── 📁 user/                        [全局偏好与画像]
     │   ├── 📄 preferences.md          (编码与交互偏好)
     │   └── 📄 profile.md              (角色设定)
     └── 📁 resources/                   [归档资源与外链]
     ```
3. **右侧 / 底部预览抽屉（L0/L1/L2 阶梯视图）**：
   - 点击文件节点，默认展示 **L0 摘要**（~100字快览）与 **L1 核心点**；
   - 提供“查看全文”按钮，按需加载 L2 Markdown 并支持一键“插入到当前输入框”，方便向 Agent 引用历史背景。

---

## 4. 记忆作用域与命名空间映射（Scoping Convention）

为了使 Codex、Hermes 和星序在同一个知识库中互不打架，统一制定如下 URI 规范：

```text
viking://
├── user/                             # 全局用户偏好与习惯（只读共享）
│   ├── memories/preferences/
│   └── profile.md
│
├── resources/projects/<project_name>/ # 项目长期知识库资源（PRD、架构设计、技术手册）
│   ├── docs/
│   └── specs/
│
├── memories/projects/<project_name>/  # 项目执行记忆（由 Agent 自动提炼生成）
│   ├── architecture-decisions/
│   ├── solved-bugs/
│   └── milestones/
│
└── sessions/<session_id>/             # 会话临时上下文与交接摘要（短暂生命周期）
```

- **项目感知（Project Awareness）**：Codex 在工作区运行时，OpenViking 插件自动将当前 Git 仓库名称映射为 `project_name`。
- **跨 Agent 共享**：Hermes 在同一项目下执行 `viking_search`，同样以该 `project_name` 作为 scope，无缝读取 Codex 刚才写入的架构经验。

---

## 5. 实施里程碑与开发计划

### Phase 1: 控制面 — 自动化配置配发服务（1-2 天）
- [ ] 编写 `core/ov_sync.py`：实现对 Local、WSL 和 Debian SSH 端的 `ovcli.conf` 与 Codex `config.toml` 检测与下发。
- [ ] 扩展前端 `UsageGatewaySettingsCard.tsx`：增加 OpenViking 记忆网关卡片，展示各端同步状态并支持一键下发。
- [ ] 实测：验证一键将配发推送到 Windows、WSL2、Debian 3 个环境，确保 Codex CLI 打开后无需手动配置即可自动加载 memory 插件。

### Phase 2: 观察面 — App Server 只读网关与缓存（1 天）
- [ ] 编写 `routers/memory.py`：集成对 OpenViking HTTP API 的只读调用（health, tree, read, search），带 2 秒超时降级。
- [ ] 编写单元测试验证无 OpenViking 服务时的优雅降级（前端不报错，仅展示离线提示）。

### Phase 3: 观察面 — Sidecar 记忆面板与交互（2 天）
- [ ] 在 `frontend/src/features/chat/sidecar/` 中实现 `MemorySidecarPanel.tsx`。
- [ ] 复用星序现有的 TreeView 组件渲染 `viking://` 节点。
- [ ] 实现 L0/L1 摘要快速预览抽屉及“引用到聊天”按钮。

---

## 6. 总结

本次架构调整确立了最干净、最稳固的集成边界：
- **Agent 本地闭环**：Codex / Hermes 自由借助官方成熟的 Hook + MCP 机制直连 OpenViking，保障 Agent 执行性能和上下文处理的纯粹性；
- **星序赋能全局**：星序作为中央控制台，负责解决多端配置分发的运维苦活，并在 Sidecar 提供美观统一的可视化看板。
