# 蜂群作战协同与监控室拓扑 (Swarm DAG) 架构与开发规范

本文档定义星序（Astrorder）**蜂群协同作战体系（Swarm Orchestration）**与**监控室拓扑大盘（Monitor Swarm DAG）**的端到端技术规范、前后端数据契约、布局渲染引擎与实施落地蓝图。

---

## 1. 架构总览与核心设计理念

传统的 AI 辅助开发模式受限于“单一窗口、单会话、单工作目录”，多 Agent 容易在狭窄上下文中互相竞争、干扰甚至爆 Token。
星序基于已有的多宿主机器拓扑（Windows 本机、本地 WSL、远端 Debian / Linux）、原生多 Agent 进程（Codex、Hermes、Grok Build）及统一 MCP 协议总线，构建**真正物理并行的蜂群协同体系**。

### 1.1 核心角色定义
- **蜂后编排器 (Lead Orchestrator / Queen)**：
  - 坐镇主控会话（通常采用高推理能力模型），负责任务分析、DAG 依赖拆解、节点派生、进度巡检与成果验收。
  - **原则**：蜂后不亲自写大段业务文件，保持自身上下文窗口精简；专门通过 MCP 驱动各个下级工蜂。
- **工蜂专职执行器 (Specialized Workers)**：
  - 针对具体的环境与专长物理隔离运行（例如远程 Debian 节点执行数据库迁移与 Linux 联调，本机执行前端组件重构）。
  - 各自拥有 100% 独立的上下文窗口与本地工作区，并发执行，互不阻塞。
- **指挥官 (Human Commander)**：
  - 坐镇星序监控室，拥有从“微观实时交互”到“宏观拓扑演化”的全局把控能力，可随时向任一节点插话、审批或终止异常节点。

---

## 2. 血缘追踪与数据契约规范

系统通过会话元数据中的父子依赖关系，天然推导出 DAG 的有向无环图结构，无需引入额外的黑盒数据库表。

### 2.1 会话血缘结构扩展
在现有的 `Session` 数据模型中，显式确立血缘与协同字段：

```typescript
interface Session {
  id: string
  agent_id: string
  title: string
  status: 'idle' | 'running' | 'waiting_approval' | 'error'
  // 血缘定义
  parent_session_id?: string | null     // 派生该会话的父会话/蜂后会话 ID
  parent_agent_id?: string | null       // 父会话归属的 Agent ID
  // 显式依赖项（可选，用于声明必须等待指定会话产出才能开始执行）
  depends_on?: string[]                  // 依赖的上游 session keys (如 ["local-codex::sess-1"])
  // 蜂群角色元数据
  swarm_role?: 'lead' | 'worker' | 'evaluator'
  task_tag?: string                      // 任务简标（如 "后端迁移"、"前端组件"、"联调测试"）
}
```

### 2.2 MCP 派生与调用规范
蜂后调用已落地的 `machines_dispatch` 或 `sessions_create` 时，大内核后端将调用者的会话环境自动注入为目标新会话的父节点：
- `source_session` ➔ 写入目标会话的 `parent_session_id` 与 `parent_agent_id`；
- 如果蜂后在调用时指定了 `depends_on` 参数，后端将其持久化入会话配置并在事件流中广播。

---

## 3. 监控室前端架构改造 (Monitor Swarm UI)

### 3.1 视图切换器升级
在 `frontend/src/features/monitor/MonitorPage.tsx` 的头部控件区，将布局模式从双态升级为三态：
```tsx
<SegmentedControl
  value={layout}
  onChange={setLayout}
  data={[
    { label: '网格', value: 'grid' },
    { label: '拓扑 (DAG)', value: 'dag' },
    { label: '列表', value: 'list' },
  ]}
/>
```

### 3.2 DAG 布局算法与渲染方案 (Swarm Canvas)
为了确保极致性能与零冗余依赖引入，不采用沉重的重型画板库，基于轻量层级分层算法（Topological Rank Layering）与 SVG 动态贝塞尔曲线实现：

1. **节点分层算法 (Rank Assignment)**：
   - 寻找没有入度的根会话（无 `parent_session_id`，通常为蜂后节点），置于 Level 0；
   - 依据 `parent_session_id` 或 `depends_on` 进行拓扑分层，依次派生 Level 1、Level 2 等；
   - 孤立会话统一归纳至“独立监控池”侧栏或悬浮停靠栏。
2. **连接线渲染 (SVG Curved Connectors with Pulse)**：
   - 节点之间使用三次贝塞尔曲线绘制平滑连线：
     ```text
     M (x1, y1) C (x1, (y1 + y2) / 2), (x2, (y1 + y2) / 2), (x2, y2)
     ```
   - **动态粒子光效**：根据上游节点的运行状态添加能量脉冲动画（`stroke-dasharray` + `stroke-dashoffset` 循环流动）。
   - **状态颜色**：
     - 上游运行中：Teal / Indigo 动态粒子流；
     - 依赖阻断/待确认：Amber 脉冲黄光；
     - 链路完成：轻量静态柔和细线。

### 3.3 拓扑节点卡片设计 (Swarm Pod Card)
DAG 中的单个卡片采用紧凑型高信息密度设计（宽度约 300px ~ 340px）：
- **顶部 Header**：
  - Agent 品牌图标（Codex / Hermes / Grok）+ 节点主机来源徽章（`[本机]`、`[Debian]`、`[WSL]`）；
  - 会话简标与蜂群角色标签（`[蜂后 Lead]` / `[工蜂 Worker]`）；
- **主体内容区**：
  - 会话标题（支持单行截断与 tooltip 悬浮展开）；
  - 当前实时活动胶囊（如：`正在运行: pytest --maxfail=1`、`思考中`、`等待审批`）；
  - 阶段进度指示与耗时计时器；
- **操作栏**：
  - 一键向右展开侧边栏即时查看该会话完整对话日志；
  - 快速停止指令（`sessions_stop`）与手动完成按钮。

---

## 4. 实时协同与双向事件协议

大内核通过 WebSocket `/ws/v1/events` 广播全局状态，前端即时驱动拓扑图演化：

| 事件类型 | 触发时机 | 前端 DAG 响应动作 |
| :--- | :--- | :--- |
| `session.upsert` | 蜂后派生新子会话 | DAG 画布上平滑弹跳插入新工蜂节点，自动建立父子连线 |
| `command.upsert` | 会话开始执行或状态改变 | 节点外框产生呼吸光环，连线触发流动光斑动画 |
| `approval.pending` | 某个工蜂产生危险操作待审批 | 节点闪烁脉冲黄色警示灯，高亮浮出审批卡片供指挥官决断 |
| `monitor.control` | Agent 动态加入/移出监控室 | 自动重排画板，聚焦至最新加入的作战簇 |

---

## 5. 实施落地步骤 (Implementation Phases)

### Phase 1: 数据模型与血缘打通 (后端)
1. 完善 `agent_gateway.py` 中的 `sessions_create` 与 `machines_dispatch`，确保创建时自动附带 `parent_session_id` 与 `parent_agent_id`。
2. 在 `public_session` 返回结构中带出父会话信息与血缘标记。

### Phase 2: 监控室 DAG 渲染引擎 (前端)
1. 在 `frontend/src/features/monitor` 下新增 `SwarmDagView.tsx` 拓扑画布组件。
2. 实现节点拓扑分层（Root ➔ Workers ➔ Evaluator）坐标计算与平滑缩放移动（Pan & Zoom）。
3. 编写 SVG 贝塞尔曲线连接器，支持运行态流动光斑特效。
4. 在 `MonitorPage.tsx` 接入 `layout === 'dag'` 渲染分支。

### Phase 3: 节点深度下钻与交互
1. 支持点击拓扑节点激活局部快速抽屉（Drawer），直接在当前页查看该工蜂节点的交互日志、输入框与审批流。
2. 支持拖拽连线手动设定依赖或解除监控关联。

---

## 6. 安全与可靠性边界
- **循环依赖防护**：拓扑计算阶段做环检测，若检测到回环依赖，自动降级为平铺或阻断报警。
- **极端节点降级**：当监控室纳入会话超过 50 个时，自动将远离视口的未激活节点收拢为折叠气泡（Cluster Pod），防止 DOM 节点过载。

