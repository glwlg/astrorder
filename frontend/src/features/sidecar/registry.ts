import { createElement, lazy, useSyncExternalStore, type ComponentType } from 'react'
import {
  IconBrain,
  IconChalkboard,
  IconChartDots,
  IconCode,
  IconFileSpreadsheet,
  IconFileTypeDocx,
  IconFileTypePdf,
  IconFolder,
  IconGitBranch,
  IconGitCompare,
  IconMessages,
  IconMovie,
  IconMusic,
  IconPhoto,
  IconPresentation,
  IconPuzzle,
  IconTerminal2,
  IconWorld,
} from '@tabler/icons-react'
import type { ArtifactRef } from '../../domain/artifact'
import { extensionRegistry } from '../extensions/extensionRegistry'
import { PluginPanelViewer } from '../extensions/SlotRenderer'
import {
  EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
  parsePluginPanelArtifact,
  type Disposable,
} from '../extensions/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import type { ArtifactViewer, ArtifactViewerComponent, ViewerContext } from './types'
import { AUDIO_MIME_TYPES, audioMediaType } from './viewers/audio/formats'
import { IMAGE_MIME_TYPES, imageMediaType } from './viewers/image/formats'
import { VIDEO_MIME_TYPES, videoMediaType } from './viewers/video/formats'

function lazyViewer<TModule extends Record<string, unknown>>(
  loader: () => Promise<TModule>,
  exportName: keyof TModule,
): ArtifactViewerComponent {
  return lazy(() =>
    loader().then((mod) => ({
      default: mod[exportName] as ComponentType<ViewerContext>,
    })),
  )
}

const LazySidecarBlackboardPanel = lazy(() =>
  import('../chat/SidecarBlackboardPanel').then((m) => ({
    default: m.SidecarBlackboardPanel,
  })),
)

function LazyBlackboardViewer({ artifact }: ViewerContext) {
  const namespace =
    artifact.sessionId && artifact.agentId
      ? `session:${artifact.agentId}::${artifact.sessionId}`
      : artifact.sessionId
        ? `session:${artifact.sessionId}`
        : 'default'
  return createElement(LazySidecarBlackboardPanel, { namespace })
}

const LazySidecarMemoryPanel = lazy(() =>
  import('./SidecarMemoryPanel').then((m) => ({
    default: m.SidecarMemoryPanel,
  })),
)

function LazyMemoryViewer({ isActive }: ViewerContext) {
  if (!isActive) return null
  return createElement(LazySidecarMemoryPanel)
}

const blackboardViewer: ArtifactViewer = {
  id: 'blackboard-viewer',
  title: '黑板',
  description: '多 Agent 任务协同共享记忆、规格参数与产出物实时看板',
  version: '0.1.0',
  source: 'builtin',
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
  component: LazyBlackboardViewer,
  documentation: {
    summary:
      '黑板是星序专为多 Agent 协同设计的分布式共享记忆总线与通信中枢。结合 json-render 视觉引擎，实现“Agent 读写纯 JSON 契约，人类操作员查看高保真交互 UI”。',
    sections: [
      {
        title: '星系命名空间物理隔离 (Namespace Scoping)',
        content:
          '星序默认按当前主星链条将数据自动隔离至 swarm:<root_session_key> 命名空间。同星系内的伴星无缝共享，不同星系之间天然物理隔离不串台。亦支持 session:<id> 私有暂存与 global 全局广播。',
      },
      {
        title: 'Agent CLI 调用规范',
        content:
          '加载 astrorder-blackboard 技能，使用技能目录内的 astrorder-agent CLI。\n1. invoke blackboard.set --input-file 请求.json：写入或更新 key、value。\n2. invoke blackboard.get --input-file 请求.json：按 key 或全量读取。\n3. invoke blackboard.delete --input-file 请求.json：删除指定 key；清空使用 key: "*"。\nCLI 自动携带当前会话身份；群聊或星图作用域在请求中显式指定 session_key。',
      },
      {
        title: '双模态呈现与即时切换',
        content:
          '每个黑板卡片均提供 [🎨 UI] 与 [{ } JSON] 双模态按钮，允许操作员在富有表现力的可视化仪表盘和原始缩进代码之间秒级切换，支持一键复制 JSON。',
      },
    ],
    supportedComponents: [
      {
        name: 'HostNodeTelemetryCard',
        label: '服务器硬件与负载体检卡',
        description:
          '以 Bento 矩阵呈现服务器系统版本、CPU 型号、1m/5m/15m 负载、物理可用内存进度条与运行时间。',
        triggerKeys: ['milestone_*_perf_ready', '包含 cpu, load, memory_available, os'],
      },
      {
        name: 'MultiNodeClusterSummary',
        label: '多节点集群性能横向对比看板',
        description:
          '左右双栏/多栏对齐横向对比 WSL、Debian 或容器集群各节点的处理器、负载、可用内存、磁盘根目录剩余及 TOP 资源消耗进程。',
        triggerKeys: ['cluster_nodes_perf_summary', '包含 wsl / debian 对象'],
      },
      {
        name: 'MissionSpecCard',
        label: '作战任务契约与指挥规格卡',
        description:
          '高对比渐变展示协同任务目标、当前推进阶段、目标机器列表（Machine ID、Agent 类型）及主星调度中枢标识。',
        triggerKeys: ['mission_spec', '包含 mission, phase, targets'],
      },
      {
        name: 'ApiEndpointsCard',
        label: 'REST / RPC 接口契约清单',
        description:
          '标准 OpenAPI/Swagger 风格，自动对 GET(绿)、POST(蓝)、PUT(黄)、DELETE(红) 染色，支持 Base URL 与状态码标记。',
        triggerKeys: ['endpoints', 'apis', 'routes'],
      },
      {
        name: 'ResourceUsageBar',
        label: '系统资源配额与负载条',
        description:
          '实时呈现 CPU、内存（已用/总量）、磁盘容量与 Pod 副本，带有阈值（绿/黄/红）平滑健康进度条。',
        triggerKeys: ['resources', 'cpu', 'memory', 'disk'],
      },
      {
        name: 'TestReport',
        label: '自动化测试与压测执行报告',
        description:
          '呈现单元测试与集成测试的 Passed / Failed / Skipped 用例数、大字号通过率百分比、执行耗时与逐项用例结果。',
        triggerKeys: ['passed, failed', 'test_results', 'suite'],
      },
      {
        name: 'CveSecurityReport',
        label: '安全审计与漏洞合规报告',
        description:
          '四级（Critical、High、Medium、Low）安全风险卡片统计，罗列 CVE 漏洞编号、受影响软件包及严重程度徽标。',
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
        description:
          '横向卡片箭头串联展示微服务、网关或协同 Agent 节点的拓扑流向与角色说明。',
        triggerKeys: ['nodes', 'edges', 'flow', 'architecture'],
      },
      {
        name: 'StepTimeline',
        label: '任务阶段与流水线时间线',
        description:
          '垂直时间线展示 CI/CD 阶段、多 Agent 协作工作流推进情况，带完成勾选、进行中雷达脉冲与失败叉号。',
        triggerKeys: ['steps', 'pipeline', 'stages', 'milestones'],
      },
      {
        name: 'DataTable',
        label: '结构化数据与清单表格',
        description:
          '支持自适应表头提取、斑马纹高亮、状态徽标自动染色与宽表水平滚动的通用数据表。',
        triggerKeys: ['rows', 'items', 'records', 'data 数组'],
      },
      {
        name: 'DiffViewer',
        label: '代码补丁与配置差异对比器',
        description:
          '以等宽排版展示统一 diff，自动对 + 行（绿）、- 行（红）、@@ 块头（紫）进行精确代码着色。',
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
        description:
          '提供 success、running、ready、warning、error 五态卡片，展示状态灯、摘要与详情键值。',
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

const memoryViewer: ArtifactViewer = {
  id: 'memory-viewer',
  title: '知识记忆',
  description: '跨 Agent 共享的项目级持久记忆与知识库，支持查看知识树与一键下发多环境 Hooks',
  version: '0.1.0',
  source: 'builtin',
  category: 'workspace',
  capabilitySlot: 'knowledge-memory',
  capabilityLabel: '知识记忆',
  providerConfigKey: 'provider',
  badgeLabel: '[记忆]',
  tabColor: 'var(--astr-indigo, #6366f1)',
  icon: IconBrain,
  extensions: ['.viking', '.ov.md'],
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-openviking-memory' ||
      artifact.id.startsWith('memory:viking')
    ) {
      return 100
    }
    return 0
  },
  capabilities: { canEdit: false },
  configOptions: [
    {
      key: 'provider',
      label: '工具实现',
      description: '选择跨 Agent 知识库与长期记忆的底层实现，可按需替换或停用',
      type: 'select',
      options: [{ label: 'OpenViking', value: 'openviking' }],
      defaultValue: 'openviking',
    },
  ],
  settingsAction: { tab: 'models', label: '配置记忆服务' },
  component: LazyMemoryViewer,
  documentation: {
    summary: '提供星序的跨 Agent 项目级持久记忆与知识库能力（当前内置 OpenViking 虚拟文件系统实现）。',
    sections: [
      {
        title: '虚拟文件系统 (viking://)',
        content:
          '提供按项目（projects/）、全局用户偏好（user/）和资源（resources/）分类的树状知识与记忆查看能力。',
      },
    ],
  },
}

const browserMirrorViewer: ArtifactViewer = {
  id: 'browser-mirror-viewer',
  title: '浏览器自动化',
  description: '驱动可视浏览器执行网页任务并在右侧实时镜像画面，支持按需切换底层自动化引擎',
  version: '1.0.0',
  source: 'builtin',
  category: 'workspace',
  capabilitySlot: 'browser-automation',
  capabilityLabel: '浏览器自动化',
  tabColor: '#3b82f6',
  icon: IconWorld,
  supports: (artifact) => (artifact.id.startsWith('browser:') ? 100 : 0),
  component: lazyViewer(() => import('./viewers/browser/BrowserMirrorViewer'), 'BrowserMirrorViewer'),
  capabilities: { canEdit: false },
}

const pdfViewer: ArtifactViewer = {
  id: 'pdf-viewer',
  title: 'PDF 阅读器',
  description:
    '调用原生渲染引擎无损预览工作区及远程 PDF 文档，支持目录缩略图、全文搜索、打印与一键引用到当前对话',
  version: '1.0.0',
  source: 'builtin',
  category: 'office',
  badgeLabel: 'PDF 文档',
  tabColor: '#dc2626',
  icon: IconFileTypePdf,
  extensions: ['.pdf'],
  mimeTypes: ['application/pdf'],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.pdf')) return 95
    if ((artifact.mediaType || '').toLowerCase() === 'application/pdf') return 90
    return 0
  },
  component: lazyViewer(() => import('./viewers/pdf/PdfViewer'), 'PdfViewer'),
  capabilities: { canEdit: false },
}

const docxViewer: ArtifactViewer = {
  id: 'docx-viewer',
  title: 'Word 文档查看与比对器',
  description:
    '零依赖解析并按 A4 纸张排版预览 .docx 文档，支持左侧目录大纲跳转、划词引用给 Agent 审阅、双版红线 Diff 与打印导出 PDF',
  version: '1.0.0',
  source: 'builtin',
  category: 'office',
  badgeLabel: 'Word 文档',
  tabColor: '#2563eb',
  icon: IconFileTypeDocx,
  extensions: ['.docx'],
  mimeTypes: ['application/vnd.openxmlformats-officedocument.wordprocessingml.document'],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.docx')) return 95
    if ((artifact.mediaType || '').toLowerCase().includes('wordprocessingml')) return 90
    return 0
  },
  component: lazyViewer(() => import('./viewers/docx/DocxViewer'), 'DocxViewer'),
  capabilities: { canEdit: false },
}

const sheetViewer: ArtifactViewer = {
  id: 'sheet-viewer',
  title: 'Excel / CSV 表格查看器',
  description:
    '零依赖解析 .xlsx 多工作表与 UTF-8/GBK 编码 .csv/.tsv，支持搜索筛选、点击列头排序、数值汇总统计及选中行发给对话分析',
  version: '1.0.0',
  source: 'builtin',
  category: 'office',
  badgeLabel: '电子表格',
  tabColor: '#15803d',
  icon: IconFileSpreadsheet,
  extensions: ['.xlsx', '.csv', '.tsv'],
  mimeTypes: [
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    'text/csv',
    'text/tab-separated-values',
  ],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.xlsx')) return 95
    if (lower.endsWith('.csv') || lower.endsWith('.tsv')) return 92
    const mime = (artifact.mediaType || '').toLowerCase()
    if (mime.includes('spreadsheetml') || mime === 'text/csv') return 88
    return 0
  },
  component: lazyViewer(() => import('./viewers/sheet/SheetViewer'), 'SheetViewer'),
  capabilities: { canEdit: false },
}

const pptxViewer: ArtifactViewer = {
  id: 'pptx-viewer',
  title: 'PPT 演示预览',
  description: '预览 PowerPoint (.pptx) 幻灯片页面、表格、插图与演讲备注，支持一键引用到对话',
  version: '1.0.0',
  source: 'builtin',
  category: 'office',
  badgeLabel: 'PPT 演示',
  tabColor: '#ea580c',
  icon: IconPresentation,
  extensions: ['.pptx'],
  mimeTypes: ['application/vnd.openxmlformats-officedocument.presentationml.presentation'],
  supports: (artifact) => {
    const name = (artifact.name || artifact.path || '').toLowerCase()
    const mime = (artifact.mediaType || '').toLowerCase()
    if (name.endsWith('.pptx')) return 95
    if (mime === 'application/vnd.openxmlformats-officedocument.presentationml.presentation') return 90
    return 0
  },
  component: lazyViewer(() => import('./viewers/pptx/PptxViewer'), 'PptxViewer'),
  capabilities: { canEdit: false },
}

const mermaidViewer: ArtifactViewer = {
  id: 'mermaid-viewer',
  title: 'Mermaid 架构图',
  description: '基于纯文本代码声明式渲染时序图、流程图与甘特图',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[架构图]',
  tabColor: '#10b981',
  icon: IconChartDots,
  extensions: ['.mmd', '.mermaid'],
  mimeTypes: ['text/vnd.mermaid'],
  component: lazyViewer(() => import('./viewers/mermaid/MermaidViewer'), 'MermaidViewer'),
  capabilities: { canEdit: true },
}

const diffViewer: ArtifactViewer = {
  id: 'diff-viewer',
  title: '代码变更比对 (Diff)',
  description: '按行呈现 Git Patch / Unified Diff 代码补丁，支持高亮与改动统计',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[Diff]',
  tabColor: '#3b82f6',
  icon: IconGitCompare,
  extensions: ['.diff', '.patch'],
  mimeTypes: ['text/x-diff'],
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'text/x-diff' ||
      artifact.id.startsWith('gitdiff:') ||
      artifact.name.endsWith('.diff') ||
      artifact.name.endsWith('.patch')
    ) {
      return 90
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/diff/DiffViewer'), 'DiffViewer'),
  capabilities: { canEdit: false },
}

const htmlViewer: ArtifactViewer = {
  id: 'html-viewer',
  title: 'HTML 页面预览',
  description: '在安全隔离的沙箱 Webview 中实时运行并渲染 HTML 交互原型与汇报页面',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[页面]',
  tabColor: '#06b6d4',
  icon: IconWorld,
  extensions: ['.html', '.htm'],
  mimeTypes: ['text/html'],
  component: lazyViewer(() => import('./viewers/html/HtmlViewer'), 'HtmlViewer'),
  capabilities: { canEdit: false },
}

const imageViewer: ArtifactViewer = {
  id: 'image-viewer',
  title: '图片查看器',
  description: '预览本地与远程图片，支持适应窗口、原始尺寸和下载',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[图片]',
  icon: IconPhoto,
  extensions: Object.keys(IMAGE_MIME_TYPES),
  supports: (artifact) =>
    imageMediaType(artifact.name) ? 75 : artifact.mediaType.startsWith('image/') ? 40 : 0,
  component: lazyViewer(() => import('./viewers/image/ImageViewer'), 'ImageViewer'),
  capabilities: { canEdit: false },
}

const videoViewer: ArtifactViewer = {
  id: 'video-viewer',
  title: '视频审片台',
  description:
    '流式播放本地与远程视频，支持截帧引用到对话、审片打点批注、逐帧步进、A-B 循环与同目录字幕联动',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[视频]',
  icon: IconMovie,
  extensions: Object.keys(VIDEO_MIME_TYPES),
  supports: (artifact) =>
    videoMediaType(artifact.name) ? 80 : artifact.mediaType.startsWith('video/') ? 45 : 0,
  component: lazyViewer(() => import('./viewers/video/VideoViewer'), 'VideoViewer'),
  capabilities: { canEdit: false },
}

const audioViewer: ArtifactViewer = {
  id: 'audio-viewer',
  title: '音频试听器',
  description: '流式试听配音、配乐与混音音轨，支持倍速、A-B 循环、同目录切轨与时间点引用',
  version: '1.0.0',
  source: 'builtin',
  category: 'media',
  badgeLabel: '[音频]',
  icon: IconMusic,
  extensions: Object.keys(AUDIO_MIME_TYPES),
  supports: (artifact) =>
    audioMediaType(artifact.name) ? 80 : artifact.mediaType.startsWith('audio/') ? 45 : 0,
  component: lazyViewer(() => import('./viewers/audio/AudioViewer'), 'AudioViewer'),
  capabilities: { canEdit: false },
}

const xtermViewer: ArtifactViewer = {
  id: 'xterm-viewer',
  title: '交互式终端',
  description: '支持 PTY 虚拟终端交互、ANSI 颜色代码回显及 Shell 命令行控制',
  version: '1.0.0',
  source: 'builtin',
  category: 'development',
  capabilitySlot: 'terminal',
  capabilityLabel: '交互终端',
  implementationName: 'Xterm.js',
  badgeLabel: '[终端]',
  tabColor: '#eab308',
  icon: IconTerminal2,
  extensions: ['.terminal'],
  mimeTypes: ['application/x-terminal'],
  component: lazyViewer(() => import('./viewers/terminal/XtermViewer'), 'XtermViewer'),
  capabilities: { canEdit: true },
  configOptions: [
    {
      key: 'fontSize',
      label: '终端字体大小',
      type: 'number',
      defaultValue: 13,
    },
    {
      key: 'cursorBlink',
      label: '光标呼吸闪烁',
      type: 'boolean',
      defaultValue: true,
    },
  ],
}

const monacoViewer: ArtifactViewer = {
  id: 'monaco-viewer',
  title: '代码与文本编辑器',
  description: '全功能代码与文本编辑器，支持语法高亮、缩略图导航、行号与修改写回',
  version: '1.0.0',
  source: 'builtin',
  category: 'development',
  capabilitySlot: 'code-editor',
  capabilityLabel: '代码编辑器',
  implementationName: 'Monaco',
  badgeLabel: '[代码]',
  tabColor: 'var(--astr-indigo)',
  icon: IconCode,
  extensions: [
    '.ts', '.tsx', '.js', '.jsx', '.py', '.json', '.rs', '.go', '.c', '.cpp',
    '.h', '.css', '.scss', '.sql', '.yaml', '.yml', '.txt', '.log', '.toml',
    '.ini', '.env', '.dockerfile', '.sh', '.bash', '.cmd', '.bat', '.ps1',
    '.md', '.markdown', '.xml', '.html.bak', '.conf', '.cfg', '.properties',
    '.template', '.vue', '.svelte', '.proto', '.graphql', '.tf', '.env',
    '.gitignore', '.editorconfig',
  ],
  mimeTypes: ['text/plain', 'application/json', 'text/javascript', 'text/x-python'],
  component: lazyViewer(() => import('./viewers/monaco/MonacoViewer'), 'MonacoViewer'),
  capabilities: { canEdit: true },
  configOptions: [
    {
      key: 'minimap',
      label: '启用代码缩略图 (Minimap)',
      type: 'boolean',
      defaultValue: true,
    },
    {
      key: 'fontSize',
      label: '代码字体大小',
      type: 'number',
      defaultValue: 13,
    },
  ],
}

const fileTreeViewer: ArtifactViewer = {
  id: 'filetree-viewer',
  title: '工作区文件浏览器',
  description: '直观浏览当前会话与工作区的目录树结构，点击文件即可在侧边栏直接打开与预览',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[目录树]',
  tabColor: '#f59e0b',
  icon: IconFolder,
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-directory' || artifact.id.startsWith('filetree:')) {
      return 100
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/filetree/FileTreeViewer'), 'FileTreeViewer'),
  capabilities: { canEdit: false },
}

const gitDiffViewer: ArtifactViewer = {
  id: 'git-diff-viewer',
  title: '代码变更',
  description: '浏览工作区未提交的文件变更列表，并查看增删行数及详细 Diff',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[Git Diff]',
  tabColor: '#3b82f6',
  icon: IconGitBranch,
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-git-diff-tree' || artifact.id.startsWith('gitdifftree:')) {
      return 100
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/gitdiff/GitDiffTreeViewer'), 'GitDiffTreeViewer'),
  capabilities: { canEdit: false },
}

const gitWorktreesViewer: ArtifactViewer = {
  id: 'git-worktrees-viewer',
  title: 'Worktree 工作区',
  description: '管理当前 Git 仓库的全部 Worktree 分支、环境配置同步、主分支 Diff 对比、安全合并与失效清理',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[Worktrees]',
  tabColor: '#0ea5e9',
  icon: IconGitBranch,
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-git-worktrees' ||
      artifact.id.startsWith('gitworktrees:')
    ) {
      return 100
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/worktrees/GitWorktreesViewer'), 'GitWorktreesViewer'),
  capabilities: { canEdit: false },
}

const sideChatViewer: ArtifactViewer = {
  id: 'sidecar-chat-viewer',
  title: '侧边聊天',
  description: '轻量并行的侧边对话窗口，支持与主会话或随身 AI 并行交互',
  version: '1.0.0',
  source: 'builtin',
  badgeLabel: '[侧边聊天]',
  tabColor: '#6366f1',
  icon: IconMessages,
  extensions: ['.sidechat'],
  mimeTypes: ['application/x-astrorder-side-chat'],
  capabilities: { canEdit: false },
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-astrorder-side-chat' ||
      artifact.id.startsWith('sidechat:')
    ) {
      return 100
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/sidechat/SideChatViewer'), 'SideChatViewer'),
}

const agentGraphViewer: ArtifactViewer = {
  id: 'agent-graph-viewer',
  title: '决策与轨迹',
  description: '按轮次聚合 Agent 阶段决策、文件影响热图、命令验证与报错自愈追踪',
  version: '2.0.0',
  source: 'builtin',
  badgeLabel: '[轨迹]',
  tabColor: '#a855f7',
  icon: IconBrain,
  extensions: ['.agentgraph'],
  mimeTypes: ['application/x-agent-graph'],
  capabilities: { canEdit: false },
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-agent-graph' ||
      artifact.id.startsWith('agentgraph:')
    ) {
      return 100
    }
    return 0
  },
  component: lazyViewer(() => import('./viewers/agentgraph/AgentGraphViewer'), 'AgentGraphViewer'),
}

const externalPluginPanelViewer: ArtifactViewer = {
  id: EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
  title: '扩展面板',
  description: '承载内置及第三方扩展注册的侧边栏工作台面板',
  source: 'builtin',
  icon: IconPuzzle,
  supports: (artifact: ArtifactRef): number => {
    const isExtMedia = Boolean(artifact.mediaType?.startsWith('application/x-astrorder-ext.'))
    const isExtId = artifact.id.startsWith('ext:')
    if (!isExtMedia && !isExtId) return 0
    const parsed = parsePluginPanelArtifact(artifact)
    if (
      parsed.pluginId &&
      extensionRegistry.getBuiltin(parsed.pluginId) &&
      !usePluginSettingsStore.getState().isPluginEnabled(parsed.pluginId)
    ) {
      return 0
    }
    return isExtMedia ? 95 : 85
  },
  component: PluginPanelViewer,
  capabilities: {
    canEdit: false,
  },
}

const NON_PREVIEWABLE_EXTENSIONS =
  /\.(?:exe|dll|msi|msix|appx|com|bin|so|dylib|a|o|obj|class|jar|war|wasm|zip|7z|rar|gz|bz2|xz|tar|woff2?|ttf|otf)$/i

export function isNonPreviewableFile(fileNameOrPath: string): boolean {
  return NON_PREVIEWABLE_EXTENSIONS.test(fileNameOrPath.toLowerCase().split(/[?#]/)[0])
}

class ArtifactViewerRegistry {
  private viewers: ArtifactViewer[] = []
  private cachedExtensionRegex: RegExp | null = null
  private cachedEnabledPlugins: Record<string, boolean> | null = null
  private listeners = new Set<() => void>()
  private version = 0

  constructor() {
    // 全部内置 Sidecar 预览器均采用轻量元数据 + React.lazy 按需加载；
    // three-viewer / excalidraw-viewer / drawio-viewer / dbx-viewer / gitlab-ci-viewer
    // 均已迁移为官方同页 ESM 热加载插件，不再静态内置于主包。
    this.register(blackboardViewer)
    this.register(memoryViewer)
    this.register(browserMirrorViewer)
    this.register(pdfViewer)
    this.register(docxViewer)
    this.register(sheetViewer)
    this.register(pptxViewer)
    this.register(mermaidViewer)
    this.register(diffViewer)
    this.register(htmlViewer)
    this.register(imageViewer)
    this.register(videoViewer)
    this.register(audioViewer)
    this.register(xtermViewer)
    this.register(monacoViewer)
    this.register(fileTreeViewer)
    this.register(gitDiffViewer)
    this.register(gitWorktreesViewer)
    this.register(externalPluginPanelViewer)
    this.register(sideChatViewer)
    this.register(agentGraphViewer)

    if (typeof usePluginSettingsStore.subscribe === 'function') {
      usePluginSettingsStore.subscribe((state, prevState) => {
        if (state.enabledPlugins !== prevState.enabledPlugins) {
          this.invalidateExtensionCache()
          this.emit()
        }
      })
    }
  }

  private invalidateExtensionCache() {
    this.cachedExtensionRegex = null
    this.cachedEnabledPlugins = null
  }

  private emit() {
    this.version += 1
    for (const listener of this.listeners) {
      listener()
    }
  }

  private isViewerEnabled(viewer: ArtifactViewer): boolean {
    const isEnabled = usePluginSettingsStore.getState().isPluginEnabled
    if (!isEnabled(viewer.id)) return false
    if (viewer.pluginId && viewer.pluginId !== viewer.id && !isEnabled(viewer.pluginId)) {
      return false
    }
    return true
  }

  register(viewer: ArtifactViewer): Disposable {
    const existingIndex = this.viewers.findIndex((v) => v.id === viewer.id)
    if (existingIndex >= 0) {
      this.viewers[existingIndex] = viewer
    } else {
      this.viewers.push(viewer)
    }
    this.invalidateExtensionCache()
    this.emit()
    return {
      dispose: () => {
        const idx = this.viewers.findIndex((v) => v === viewer)
        if (idx >= 0) {
          this.viewers.splice(idx, 1)
          this.invalidateExtensionCache()
          this.emit()
        }
      },
    }
  }

  unregister(id: string): boolean {
    const idx = this.viewers.findIndex((v) => v.id === id)
    if (idx < 0) return false
    this.viewers.splice(idx, 1)
    this.invalidateExtensionCache()
    this.emit()
    return true
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  getSnapshotVersion(): number {
    return this.version
  }

  getAllViewers(): ArtifactViewer[] {
    return [...this.viewers]
  }

  /**
   * 收集所有已启用插件声明的扩展名集合（包括内置基础后缀）
   */
  getAllRegisteredExtensions(): Set<string> {
    const exts = new Set<string>([
      '.png', '.jpg', '.jpeg', '.webp', '.svg', '.gif', '.bmp', '.ico', '.exe',
    ])
    for (const v of this.viewers) {
      if (!this.isViewerEnabled(v)) continue
      if (v.extensions) {
        for (const ext of v.extensions) {
          exts.add(ext.toLowerCase())
        }
      }
    }
    return exts
  }

  /**
   * 基于所有已启用插件自声明扩展名，动态编译生成精准识别正则
   */
  getExtensionPattern(): RegExp {
    const currentEnabled = usePluginSettingsStore.getState().enabledPlugins
    if (this.cachedExtensionRegex && this.cachedEnabledPlugins === currentEnabled) {
      return this.cachedExtensionRegex
    }

    const exts = Array.from(this.getAllRegisteredExtensions())
      .map((ext) => ext.replace(/^\./, '').replace(/\./g, '\\.'))
      .sort((a, b) => b.length - a.length) // 优先匹配较长后缀（如 drawio.xml 优先于 xml）

    if (exts.length === 0) {
      this.cachedExtensionRegex = /$.^/ // 永远不匹配
    } else {
      const pattern = `([\\w\\u4e00-\\u9fa5\\-_./\\\\](?:[\\w\\u4e00-\\u9fa5\\-_./\\\\ ]*?[\\w\\u4e00-\\u9fa5\\-_./\\\\])?\\.(?:${exts.join('|')}))(?=[\\s,，。！!？?\\)）\\]】"'<>]|\\b|$)`
      this.cachedExtensionRegex = new RegExp(pattern, 'gi')
    }
    this.cachedEnabledPlugins = currentEnabled

    return this.cachedExtensionRegex
  }

  /**
   * 判断某个文件名/路径是否能被注册表中已启用的插件处理
   */
  isSupportedExtension(fileNameOrPath: string): boolean {
    const lower = fileNameOrPath.toLowerCase().split(/[?#]/)[0]
    for (const ext of this.getAllRegisteredExtensions()) {
      if (lower.endsWith(ext.toLowerCase())) {
        return true
      }
    }
    return false
  }

  findViewer(artifact: ArtifactRef): ArtifactViewer | null {
    let bestViewer: ArtifactViewer | null = null
    let maxPriority = 0

    const lowerName = (artifact.name || '').toLowerCase()
    const mime = (artifact.mediaType || '').toLowerCase()

    for (const viewer of this.viewers) {
      // 用户关闭开关则跳过
      if (!this.isViewerEnabled(viewer)) continue

      let priority = 0

      // 1. 优先调用自定义打分支持
      if (viewer.supports) {
        priority = viewer.supports(artifact)
      } else {
        // 2. 默认自声明匹配：复合扩展名（如 .drawio.xml） > 专用单扩展名 > 通用文本编辑器 > MIME 匹配
        const matchedExts = viewer.extensions?.filter((ext) => lowerName.endsWith(ext.toLowerCase())) || []
        if (matchedExts.length > 0) {
          const maxExtLen = Math.max(...matchedExts.map((ext) => ext.length))
          const hasCompoundExt = matchedExts.some((ext) => ext.replace(/^\./, '').includes('.'))
          if (hasCompoundExt) {
            priority = 85 + Math.min(10, maxExtLen)
          } else {
            priority = viewer.id === 'monaco-viewer' ? 80 : 82
          }
        } else if (
          viewer.mimeTypes?.some((m) => m.toLowerCase() === mime && mime !== 'text/plain')
        ) {
          priority = 40
        }
      }

      if (priority > maxPriority) {
        maxPriority = priority
        bestViewer = viewer
      }
    }

    // 未知扩展名及无扩展名文件也允许尝试文本读取，由编辑器检查实际内容。
    const monaco = this.getViewerById('monaco-viewer')
    const mayBeText = !mime || mime.startsWith('text/') || mime === 'application/octet-stream'
    if (
      !bestViewer &&
      monaco &&
      this.isViewerEnabled(monaco) &&
      !isNonPreviewableFile(lowerName) &&
      mayBeText
    ) {
      return monaco
    }

    return bestViewer
  }

  getViewerById(id: string): ArtifactViewer | null {
    return this.viewers.find((v) => v.id === id) || null
  }
}

export const artifactViewerRegistry = new ArtifactViewerRegistry()

export function useViewerRegistryVersion(): number {
  return useSyncExternalStore(
    (onStoreChange) => artifactViewerRegistry.subscribe(onStoreChange),
    () => artifactViewerRegistry.getSnapshotVersion(),
    () => artifactViewerRegistry.getSnapshotVersion(),
  )
}
