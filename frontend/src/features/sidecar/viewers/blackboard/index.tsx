import { IconChalkboard } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer, ViewerContext } from '../../types'
import { SidecarBlackboardPanel } from '../../../chat/SidecarBlackboardPanel'

export const blackboardViewer: ArtifactViewer = {
  id: 'blackboard-viewer',
  title: '黑板',
  description: '多 Agent 任务协同共享记忆、规格参数与产出物实时看板',
  version: '0.1.0',
  badgeLabel: '[黑板]',
  tabColor: 'var(--astr-indigo, #6366f1)',
  icon: IconChalkboard,
  extensions: ['.blackboard', '.bb.json'],
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-blackboard' ||
      artifact.id.startsWith('blackboard:')
    ) {
      return 100
    }
    return 0
  },
  capabilities: { canEdit: true },
  component: BlackboardViewerComponent,
  documentation: {
    summary: '黑板是星序专为多 Agent 协同设计的分布式共享记忆总线与通信中枢。结合 json-render 视觉引擎，实现“Agent 读写纯 JSON 契约，人类操作员查看高保真交互 UI”。',
    sections: [
      {
        title: '星系命名空间物理隔离 (Namespace Scoping)',
        content: '星序默认按当前主星链条将数据自动隔离至 swarm:<root_session_key> 命名空间。同星系内的伴星无缝共享，不同星系之间天然物理隔离不串台。亦支持 session:<id> 私有暂存与 global 全局广播。',
      },
      {
        title: 'Agent 原生 MCP 接口协议规范',
        content: '1. mcp__astrorder__blackboard_set: 写入或更新变量（key, value, 可选 namespace）\n2. mcp__astrorder__blackboard_get: 按键或全量列出黑板变量（key, 可选 namespace）\n3. mcp__astrorder__blackboard_delete: 删除单项或一键彻底清空整个星系空间（key: "*" 或 clean_namespace: true）',
      },
      {
        title: '双模态呈现与即时切换',
        content: '每个黑板卡片均提供 [🎨 UI] 与 [{ } JSON] 双模态按钮，允许操作员在富有表现力的可视化仪表盘和原始缩进代码之间秒级切换，支持一键复制 JSON。',
      },
    ],
    supportedComponents: [
      {
        name: 'HostNodeTelemetryCard',
        label: '服务器硬件与负载体检卡',
        description: '以 Bento 矩阵呈现服务器系统版本、CPU 型号、1m/5m/15m 负载、物理可用内存进度条与运行时间。',
        triggerKeys: ['milestone_*_perf_ready', '包含 cpu, load, memory_available, os'],
      },
      {
        name: 'MultiNodeClusterSummary',
        label: '多节点集群性能横向对比看板',
        description: '左右双栏/多栏对齐横向对比 WSL、Debian 或容器集群各节点的处理器、负载、可用内存、磁盘根目录剩余及 TOP 资源消耗进程。',
        triggerKeys: ['cluster_nodes_perf_summary', '包含 wsl / debian 对象'],
      },
      {
        name: 'MissionSpecCard',
        label: '作战任务契约与指挥规格卡',
        description: '高对比渐变展示协同任务目标、当前推进阶段、目标机器列表（Machine ID、Agent 类型）及主星调度中枢标识。',
        triggerKeys: ['mission_spec', '包含 mission, phase, targets'],
      },
      {
        name: 'ApiEndpointsCard',
        label: 'REST / RPC 接口契约清单',
        description: '标准 OpenAPI/Swagger 风格，自动对 GET(绿)、POST(蓝)、PUT(黄)、DELETE(红) 染色，支持 Base URL 与状态码标记。',
        triggerKeys: ['endpoints', 'apis', 'routes'],
      },
      {
        name: 'ResourceUsageBar',
        label: '系统资源配额与负载条',
        description: '实时呈现 CPU、内存（已用/总量）、磁盘容量与 Pod 副本，带有阈值（绿/黄/红）平滑健康进度条。',
        triggerKeys: ['resources', 'cpu', 'memory', 'disk'],
      },
      {
        name: 'TestReport',
        label: '自动化测试与压测执行报告',
        description: '呈现单元测试与集成测试的 Passed / Failed / Skipped 用例数、大字号通过率百分比、执行耗时与逐项用例结果。',
        triggerKeys: ['passed, failed', 'test_results', 'suite'],
      },
      {
        name: 'CveSecurityReport',
        label: '安全审计与漏洞合规报告',
        description: '四级（Critical、High、Medium、Low）安全风险卡片统计，罗列 CVE 漏洞编号、受影响软件包及严重程度徽标。',
        triggerKeys: ['vulnerabilities', 'critical, high', 'cve'],
      },
      {
        name: 'GitCommitLog',
        label: 'Git 提交与发布变更日志',
        description: '以代码版本树样式展示分支、短 commit SHA、提交信息、提交作者及相对时间戳。',
        triggerKeys: ['commits', 'git_log', 'changelog'],
      },
      {
        name: 'ArchitectureFlow',
        label: '微服务调用链路与架构拓扑图',
        description: '横向卡片箭头串联展示微服务、网关或协同 Agent 节点的拓扑流向与角色说明。',
        triggerKeys: ['nodes', 'edges', 'flow', 'architecture'],
      },
      {
        name: 'StepTimeline',
        label: '任务阶段与流水线时间线',
        description: '垂直时间线展示 CI/CD 阶段、多 Agent 协作工作流推进情况，带完成勾选、进行中雷达脉冲与失败叉号。',
        triggerKeys: ['steps', 'pipeline', 'stages', 'milestones'],
      },
      {
        name: 'DataTable',
        label: '结构化数据与清单表格',
        description: '支持自适应表头提取、斑马纹高亮、状态徽标自动染色与宽表水平滚动的通用数据表。',
        triggerKeys: ['rows', 'items', 'records', 'data 数组'],
      },
      {
        name: 'DiffViewer',
        label: '代码补丁与配置差异对比器',
        description: '以等宽排版展示统一 diff，自动对 + 行（绿）、- 行（红）、@@ 块头（紫）进行精确代码着色。',
        triggerKeys: ['diff', 'patch'],
      },
      {
        name: 'Checklist',
        label: '执行清单与交付验收表',
        description: '展示验收标准与检查项，已完成项自动划线与变灰，可关联负责人标签。',
        triggerKeys: ['checklist', 'todos', 'tasks'],
      },
      {
        name: 'TerminalLog',
        label: '控制台终端日志流',
        description: '深色控制台终端样式，自动对 ERROR、WARN、fail 关键字高亮染色。',
        triggerKeys: ['logs', 'stdout', 'output'],
      },
      {
        name: 'StatusCard',
        label: '服务与网关状态概览卡片',
        description: '提供 success、running、ready、warning、error 五态卡片，展示状态灯、摘要与详情键值。',
        triggerKeys: ['status', 'state', 'error', 'result'],
      },
      {
        name: 'KeyValueGrid',
        label: '通用 Bento 配置网格',
        description: '将各类常规对象以紧凑平铺的双列/三列药丸卡片呈现，杜绝单调的长 JSON。',
        triggerKeys: ['通用对象与字典参数'],
      },
      {
        name: 'GomokuBoard',
        label: '五子棋拟物对弈棋盘彩蛋',
        description: '15×15 真实原木纹理棋盘，3D 黑白立体棋子、天元星位与绝杀手金色光环。',
        triggerKeys: ['gomoku', 'ascii_board', 'winning_move'],
      },
    ],
  },
}

export function BlackboardViewerComponent({ artifact }: ViewerContext) {
  const namespace = artifact.sessionId && artifact.agentId
    ? `session:${artifact.agentId}::${artifact.sessionId}`
    : artifact.sessionId ? `session:${artifact.sessionId}` : 'default'
  return <SidecarBlackboardPanel namespace={namespace} />
}
