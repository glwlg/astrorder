import type { ComponentType } from 'react'
import {
  IconArchive,
  IconChartBar,
  IconCompass,
  IconGitBranch,
  IconHistory,
  IconId,
  IconLayoutGrid,
  IconMicrophone,
  IconNotebook,
  IconPlugConnected,
  IconTerminal,
  IconUsers,
} from '@tabler/icons-react'
import type { PluginCategory, ViewerConfigOption } from '../sidecar/types'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID } from './pluginSettingsStore'

export { SIDEBAR_INTEGRATIONS_PLUGIN_ID }

// 原生 Agent 引擎插件注册表（单一来源见 agentEngineRegistry.ts）
export {
  AGENT_ANTIGRAVITY_PLUGIN_ID,
  AGENT_CODEX_PLUGIN_ID,
  AGENT_ENGINE_REGISTRY,
  AGENT_GROK_PLUGIN_ID,
  AGENT_HERMES_PLUGIN_ID,
  BUILTIN_AGENT_PLUGINS,
  agentPluginIdForKind,
  canonicalAgentKind,
  isAgentKindEnabled,
  lookupAgentEngine,
  type AgentEnginePluginDescriptor,
  type ManagedAgentKind,
  type RelatedPluginLink,
} from './agentEngineRegistry'

// 系统能力与工作流插件 ID
export const COMMAND_COMPRESSION_PLUGIN_ID = 'command-compression'
export const VOICE_INPUT_PLUGIN_ID = 'voice-input'
export const WORKFLOW_GIT_WORKTREE_PLUGIN_ID = 'workflow-git-worktree'
export const CODE_CHECKPOINTS_PLUGIN_ID = 'code-checkpoints'
export const DEFAULT_MAX_AUTO_CHECKPOINTS = 20

// 主工作台扩展模块插件 ID（关闭后隐藏主导航入口并停止对应后台轮询）
export const WORKSPACE_BACKUPS_PLUGIN_ID = 'workspace-backups'
export const WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID = 'workspace-agent-accounts'
export const WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID = 'workspace-analytics-gateway'
export const WORKSPACE_GROUPS_PLUGIN_ID = 'workspace-groups'
export const WORKSPACE_MONITOR_PLUGIN_ID = 'workspace-monitor'
export const WORKSPACE_DIGEST_PLUGIN_ID = 'workspace-digest'
export const WORKSPACE_MEETINGS_PLUGIN_ID = 'workspace-meetings'
export const WORKSPACE_LEARNING_RADAR_PLUGIN_ID = 'workspace-learning-radar'

// 内置 Sidecar 查看器插件 ID 常量
export const MEMORY_VIEWER_PLUGIN_ID = 'memory-viewer'
export const BROWSER_VIEWER_PLUGIN_ID = 'browser-mirror-viewer'
export const BLACKBOARD_VIEWER_PLUGIN_ID = 'blackboard-viewer'
export const FILETREE_VIEWER_PLUGIN_ID = 'filetree-viewer'
export const XTERM_VIEWER_PLUGIN_ID = 'xterm-viewer'
export const SIDECHAT_VIEWER_PLUGIN_ID = 'sidecar-chat-viewer'
export const AGENT_GRAPH_VIEWER_PLUGIN_ID = 'agent-graph-viewer'

export interface SystemPluginDescriptor {
  id: string
  title: string
  description: string
  category: PluginCategory
  icon: ComponentType<{ size?: number }>
  badgeLabel?: string
  capabilitySlot?: string
  capabilityLabel?: string
  implementationName?: string
  providerConfigKey?: string
  configOptions?: ViewerConfigOption[]
  settingsAction?: { tab: string; label: string }
  routeAction?: { to: string; label: string }
  searchKeywords: string[]
}

export const BUILTIN_SYSTEM_PLUGINS: SystemPluginDescriptor[] = [
  {
    id: COMMAND_COMPRESSION_PLUGIN_ID,
    title: '命令输出压缩',
    description: '自动压缩 Agent 读取的命令行输出，减少上下文 Token 冗余消耗；停用后隐藏设置中的独立配置页',
    category: 'runtime',
    icon: IconTerminal,
    capabilitySlot: 'command-compression',
    capabilityLabel: '命令压缩',
    providerConfigKey: 'provider',
    configOptions: [
      {
        key: 'provider',
        label: '工具实现',
        description: '选择底层命令行输出压缩工具实现，可按需替换或停用',
        type: 'select',
        options: [{ label: 'RTK', value: 'rtk' }],
        defaultValue: 'rtk',
      },
    ],
    settingsAction: { tab: 'rtk', label: '管理压缩配置' },
    searchKeywords: ['rtk', '压缩', '命令行', 'token', 'context', 'compression'],
  },
  {
    id: CODE_CHECKPOINTS_PLUGIN_ID,
    title: '代码检查点',
    description: '按对话轮次自动记录零污染 Git 工作区快照，支持单文件/整仓差异比对与一键安全回滚；关闭后停止自动捕获并隐藏会话检查点入口',
    category: 'development',
    icon: IconHistory,
    badgeLabel: '开发工作流',
    configOptions: [
      {
        key: 'maxAutoSnapshots',
        label: '自动保留快照数',
        description: '每个会话最多自动保留的轮次快照数（默认 20，范围 1–200；手动保存与回滚前备份不受此上限影响）',
        type: 'number',
        defaultValue: DEFAULT_MAX_AUTO_CHECKPOINTS,
        min: 1,
        max: 200,
        step: 5,
      },
      {
        key: 'autoCapture',
        label: '每轮自动捕获快照',
        description: '关闭后不再在每轮对话前后自动生成快照，仍可手动保存与回滚',
        type: 'boolean',
        defaultValue: true,
      },
    ],
    searchKeywords: ['checkpoint', 'snapshot', 'git', 'rollback', '检查点', '快照', '回滚', '版本'],
  },
  {
    id: WORKFLOW_GIT_WORKTREE_PLUGIN_ID,
    title: 'Git Worktree 分叉工作区',
    description: '支持从任意会话或回复在独立 Git Worktree 目录创建平行分支；习惯单目录开发时可关闭以精简会话菜单',
    category: 'development',
    icon: IconGitBranch,
    badgeLabel: '开发工作流',
    searchKeywords: ['git', 'worktree', '工作树', '分支', '分叉', 'fork'],
  },
  {
    id: SIDEBAR_INTEGRATIONS_PLUGIN_ID,
    title: '侧边栏外链集成',
    description: '在侧边栏挂载第三方网关、监控面板、文档站等外链菜单，自动抓取站点图标并支持自定义图标；桌面端采用独立原生视图保留登录态',
    category: 'workspace',
    icon: IconPlugConnected,
    badgeLabel: '工作台插件',
    settingsAction: { tab: 'integrations', label: '管理集成' },
    searchKeywords: ['sidebar', 'integrations', '外链', '网关', '监控面板', '集成'],
  },
  {
    id: WORKSPACE_GROUPS_PLUGIN_ID,
    title: '多 Agent 群聊协作',
    description: '将多个 Agent 拉入同一群聊房间进行接力协作与共享上下文；专注单会话开发时可关闭以精简左侧导航',
    category: 'workspace',
    icon: IconUsers,
    badgeLabel: '导航模块',
    routeAction: { to: '/groups', label: '进入群聊' },
    searchKeywords: ['groups', '群聊', '多agent', '接力', '协作'],
  },
  {
    id: WORKSPACE_MONITOR_PLUGIN_ID,
    title: '多会话监控矩阵',
    description: '在多窗格矩阵视图中同时观察并调度多个会话实时状态；关闭后隐藏左侧「矩阵」导航入口',
    category: 'workspace',
    icon: IconLayoutGrid,
    badgeLabel: '导航模块',
    routeAction: { to: '/monitor', label: '进入矩阵' },
    searchKeywords: ['monitor', 'matrix', '矩阵', '监控', '多窗'],
  },
  {
    id: WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
    title: 'LLM 网关同步与用量统计',
    description: '对接模型中转网关同步多环境模型目录，并展示每日 Token 用量统计；直连官方模型时可关闭以停止后台轮询',
    category: 'workspace',
    icon: IconChartBar,
    capabilitySlot: 'llm-gateway-analytics',
    capabilityLabel: '模型网关',
    implementationName: 'OpenCodeX / Magpie',
    settingsAction: { tab: 'models', label: '配置网关同步' },
    routeAction: { to: '/analytics', label: '查看统计' },
    searchKeywords: ['analytics', 'usage', 'token', 'gateway', 'opencodex', 'magpie', '网关', '用量', '统计'],
  },
  {
    id: WORKSPACE_BACKUPS_PLUGIN_ID,
    title: '定时备份与快照',
    description: '按计划备份指定项目目录与文件并支持版本回滚和恢复钩子；不需要定时备份时可关闭以隐藏左侧导航',
    category: 'workspace',
    icon: IconArchive,
    badgeLabel: '导航模块',
    routeAction: { to: '/backups', label: '管理备份' },
    searchKeywords: ['backups', '备份', '快照', '定时', '恢复'],
  },
  {
    id: WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID,
    title: 'Agent 多账号与配额池',
    description: '集中管理多账号切换与额度刷新（当前内置 Antigravity 平台）；关闭后隐藏导航并停止后台配额定时刷新',
    category: 'workspace',
    icon: IconId,
    capabilitySlot: 'agent-accounts',
    capabilityLabel: '账号配额',
    implementationName: 'Antigravity',
    routeAction: { to: '/agent-accounts', label: '管理账号' },
    searchKeywords: ['accounts', 'quota', 'antigravity', 'agy', '账号', '配额', '额度'],
  },
  {
    id: WORKSPACE_DIGEST_PLUGIN_ID,
    title: '智能工作复盘与周报',
    description: '按时间跨度自动汇总跨项目会话、Git 提交、任务检查点与 SQL 变更记录，套用多套汇报模板一键生成日报/周报/技术复盘并导出 Word、PPT 或 PDF',
    category: 'office',
    icon: IconNotebook,
    badgeLabel: '办公工作台',
    routeAction: { to: '/digest', label: '打开复盘' },
    searchKeywords: ['digest', 'weekly', 'daily', 'report', '复盘', '周报', '日报', '汇报', '总结', '办公'],
  },
  {
    id: WORKSPACE_MEETINGS_PLUGIN_ID,
    title: '会议纪要与行动项',
    description: '本机离线实时边录边转写与独立悬浮胶囊，在会议页内自由选用已配置的 AI 供应商模型边听边总结，并与待办收件箱联动跟踪',
    category: 'office',
    icon: IconMicrophone,
    badgeLabel: '办公工作台',
    routeAction: { to: '/meetings', label: '打开会议' },
    searchKeywords: ['meeting', 'minutes', 'audio', 'action', 'r2t2', 'model', '会议', '纪要', '录音', '总结模型', '行动项', '待办', '办公'],
  },
  {
    id: WORKSPACE_LEARNING_RADAR_PLUGIN_ID,
    title: '成长雷达（Bot 驱动主动学习）',
    description: '每周定时或手动派发成长导师 Bot 扫描你的真实会话、Git 改动与 CI 失败痕迹，提炼 AI 代写黑盒与高频卡点，提供代码伴学、间隔复习与移动端竖滑刷卡信息流',
    category: 'workspace',
    icon: IconCompass,
    badgeLabel: '成长工作台',
    routeAction: { to: '/learning', label: '打开成长雷达' },
    searchKeywords: ['learning', 'radar', 'mentor', 'study', 'review', 'flashcard', '成长', '雷达', '学习', '复习', '导师', '刷卡'],
  },
]
