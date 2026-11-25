import { buildPersonaPromptBlock } from './officePersona'
import type { OfficePersonaProfile } from '../../api/client'

export interface OfficeTemplateField {
  key: string
  label: string
  placeholder: string
  multiline?: boolean
  required?: boolean
  options?: string[]
  defaultValue?: string
}

export interface OfficeDraftTemplate {
  id: string
  name: string
  shortLabel: string
  category: '文档起草' | '演示汇报' | '对外沟通' | '合规分析'
  description: string
  outputExt: 'md' | 'pptx' | 'xlsx'
  defaultFileNamePrefix: string
  fields: OfficeTemplateField[]
  buildPrompt: (values: Record<string, string>) => { title: string; instruction: string }
}

export const OFFICE_DRAFT_TEMPLATES: OfficeDraftTemplate[] = [
  {
    id: 'proposal',
    name: '汇报方案 / 立项书',
    shortLabel: '方案立项',
    category: '文档起草',
    description: '生成含背景、目标、核心方案、里程碑排期、预算与风险应对的正式汇报方案',
    outputExt: 'md',
    defaultFileNamePrefix: '推进方案',
    fields: [
      { key: 'topic', label: '方案主题', placeholder: '例如：Q4 政企大客户智能运营平台升级方案', required: true },
      { key: 'audience', label: '汇报对象', placeholder: '例如：公司执委会 / 业务分管领导', defaultValue: '公司管理层' },
      { key: 'goal', label: '核心目标与预期收益', placeholder: '例如：将人工整理周期缩短 50%，提升续费率至 92%', multiline: true },
      { key: 'constraints', label: '关键事实 / 排期 / 预算约束', placeholder: '可补充时间节点、资源投入或已有数据…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: v.topic?.trim() || '项目推进方案',
      instruction: [
        `请起草一份面向「${v.audience || '管理层'}」的正式方案：《${v.topic || '项目推进方案'}》。`,
        v.goal ? `核心目标与收益：${v.goal}` : '',
        v.constraints ? `约束与补充事实：${v.constraints}` : '',
        '输出结构要求：1. 执行摘要（3条核心结论）；2. 背景与痛点；3. 核心方案设计；4. 实施里程碑排期表（Markdown 表格）；5. 资源/预算与风险应对矩阵。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'ppt_deck',
    name: 'PPT 演示大纲',
    shortLabel: 'PPT 大纲',
    category: '演示汇报',
    description: '逐页生成标题、精炼要点与图表配图建议，成稿后可一键导出为 .pptx 文件',
    outputExt: 'pptx',
    defaultFileNamePrefix: '汇报PPT大纲',
    fields: [
      { key: 'topic', label: '演示主题', placeholder: '例如：2026 年度数字化运营成果与明年规划', required: true },
      { key: 'pages', label: '目标页数', placeholder: '8-12 页', options: ['6 页（精简快报）', '10 页（标准汇报）', '15 页（深度评审）'], defaultValue: '10 页（标准汇报）' },
      { key: 'audience', label: '听众角色', placeholder: '例如：管理层 / 外部客户 / 跨部门评审', defaultValue: '管理层' },
      { key: 'points', label: '必须包含的核心亮点与数据', placeholder: '列出关键数据、案例或必讲结论…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: v.topic?.trim() || '汇报演示文稿',
      instruction: [
        `请规划一份 ${v.pages || '10 页'} 的高管汇报 PPT 大纲：《${v.topic || '汇报演示文稿'}》，面向「${v.audience || '管理层'}」。`,
        v.points ? `必须涵盖的关键亮点与数据：${v.points}` : '',
        '格式要求：每一页使用 `## 第 X 页：页面主标题（带结论）` 开头，下面给 3-5 条凝练项目符号（每条 ≤ 25 字），并附一条「配图/图表建议」，方便直接一键导出为 PowerPoint (.pptx)。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'official_notice',
    name: '公文 / 通知公告',
    shortLabel: '公文通知',
    category: '文档起草',
    description: '规范正式的内部通知、请示、函件或制度发布公告，措辞严谨可直接下发',
    outputExt: 'md',
    defaultFileNamePrefix: '通知公告',
    fields: [
      { key: 'docType', label: '公文类型', placeholder: '通知', options: ['通知', '请示', '工作函', '会议通知', '表扬/通报'], defaultValue: '通知' },
      { key: 'topic', label: '发文事由 / 标题', placeholder: '例如：关于开展 2026 年第四季度信息安全自查的通知', required: true },
      { key: 'details', label: '核心要求与时间节点', placeholder: '明确执行范围、截止时间、提交方式与联系人…', multiline: true, required: true },
      { key: 'issuer', label: '落款部门', placeholder: '例如：综合管理部' },
    ],
    buildPrompt: (v) => ({
      title: v.topic?.trim() || `${v.docType || '工作通知'}`,
      instruction: [
        `请起草一份正式规范的「${v.docType || '通知'}」：《${v.topic || '工作通知'}》。`,
        `核心事项与要求：${v.details || ''}`,
        v.issuer ? `落款部门：${v.issuer}` : '',
        '要求：行文符合规范中文公文口径，主送、正文（一、二、三层级清晰）、执行时间节点与联系人、落款日期齐全，无口语化表述。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'business_message',
    name: '商务邮件 / 飞书钉钉消息',
    shortLabel: '邮件消息',
    category: '对外沟通',
    description: '一键生成得体专业的邮件或即时消息，支持正式、委婉、催办、英文商务等语气',
    outputExt: 'md',
    defaultFileNamePrefix: '沟通文案',
    fields: [
      { key: 'channel', label: '发送渠道与语气', placeholder: '正式邮件', options: ['正式商务邮件', '飞书/钉钉精炼消息', '委婉催办/跟进', '向上汇报简讯', '英文商务邮件'], defaultValue: '正式商务邮件' },
      { key: 'recipient', label: '收件人 / 对方身份', placeholder: '例如：客户项目总监 / 兄弟部门负责人', required: true },
      { key: 'purpose', label: '沟通目的与关键信息', placeholder: '例如：同步本周验收延期 2 天的原因，并确认新的联调窗口…', multiline: true, required: true },
    ],
    buildPrompt: (v) => ({
      title: `${v.channel || '商务沟通'} - ${v.recipient || ''}`.trim(),
      instruction: [
        `请起草一封「${v.channel || '正式商务邮件'}」，发送对象为「${v.recipient || '对方'}」。`,
        `沟通目的与关键信息：${v.purpose || ''}`,
        '要求：提供 1 个推荐主版本（含建议主题行 + 称呼 + 正文 + 明确下一步行动与落款）以及 1 个更简短的备选版本，便于直接复制发送。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'contract_review',
    name: '合同 / 协议风险审查',
    shortLabel: '合同审查',
    category: '合规分析',
    description: '按我方立场排查付款、违约、知识产权、保密与交付验收条款风险并给出修订措辞',
    outputExt: 'md',
    defaultFileNamePrefix: '合同审查意见',
    fields: [
      { key: 'contractType', label: '合同 / 协议类型', placeholder: '例如：SaaS 软件采购合同 / 技术服务协议', required: true },
      { key: 'stance', label: '我方立场', placeholder: '采购方（甲方）', options: ['采购方（甲方）', '服务/供应方（乙方）', '合作共建方'], defaultValue: '采购方（甲方）' },
      { key: 'focus', label: '重点关注条款或原文摘录', placeholder: '可粘贴关键条款，或先在工作空间勾选合同 .docx 资料包…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: `${v.contractType || '合同'}风险审查报告`,
      instruction: [
        `请以「${v.stance || '采购方（甲方）'}」立场对「${v.contractType || '合同协议'}」进行专业风险审查。`,
        v.focus ? `重点关注/摘录内容：\n${v.focus}` : '',
        '输出结构：1. 总体审查结论；2. 风险清单汇总表（条款/位置、风险等级 高/中/低、风险原因、建议修订条款）；3. 缺失保护条款补充建议。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'sop_guide',
    name: 'SOP 流程 / 操作制度',
    shortLabel: 'SOP 制度',
    category: '文档起草',
    description: '梳理标准操作流程、角色 RACI 分工、检查清单与异常处理机制',
    outputExt: 'md',
    defaultFileNamePrefix: 'SOP流程规范',
    fields: [
      { key: 'topic', label: '流程名称', placeholder: '例如：新客户上线交付标准操作流程 (SOP)', required: true },
      { key: 'roles', label: '涉及角色 / 部门', placeholder: '例如：销售、交付顾问、财务、客户经理' },
      { key: 'steps', label: '关键环节与注意事项', placeholder: '简述现有步骤、常见出错点或 SLA 要求…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: v.topic?.trim() || '标准操作流程 SOP',
      instruction: [
        `请编写一份可落地执行的标准操作流程文档：《${v.topic || '标准操作流程 SOP'}》。`,
        v.roles ? `涉及角色/部门：${v.roles}` : '',
        v.steps ? `核心环节与要求：${v.steps}` : '',
        '输出结构：1. 适用范围与目标；2. 角色与职责分工表（Markdown 表格）；3. 分阶段标准操作步骤（含输入/动作/产出/时限）；4. 异常兜底与交付验收检查清单（复选框）。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'decision_matrix',
    name: '方案对比 / 决策评估表',
    shortLabel: '对比决策表',
    category: '合规分析',
    description: '针对多个供应商或备选方案生成量化评分对比表与管理层推荐意见（可导出 Excel）',
    outputExt: 'xlsx',
    defaultFileNamePrefix: '方案对比评估',
    fields: [
      { key: 'topic', label: '评估主题', placeholder: '例如：三家云厂商大模型网关采购选型评估', required: true },
      { key: 'options', label: '备选方案 / 供应商清单', placeholder: '例如：方案 A、方案 B、方案 C', required: true },
      { key: 'criteria', label: '核心对比维度与已知数据', placeholder: '例如：成本、安全性、交付周期、运维复杂度…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: v.topic?.trim() || '方案对比评估报告',
      instruction: [
        `请围绕「${v.topic || '方案选型'}」对以下备选方案进行系统对比评估：${v.options || ''}。`,
        v.criteria ? `评估维度与已知资料：${v.criteria}` : '',
        '输出结构：1. 核心推荐结论（1段话直给推荐方案及理由）；2. 量化对比评分表（Markdown 表格，含权重与总分，便于一键导出 Excel）；3. 各方案优劣势与潜在风险提示。',
      ].filter(Boolean).join('\n'),
    }),
  },
  {
    id: 'retro_summary',
    name: '项目复盘 / 阶段总结',
    shortLabel: '项目复盘',
    category: '演示汇报',
    description: '基于 GRAI 复盘框架沉淀目标达成、亮点经验、根因教训与下阶段改进清单',
    outputExt: 'md',
    defaultFileNamePrefix: '项目复盘报告',
    fields: [
      { key: 'topic', label: '项目 / 活动名称', placeholder: '例如：双十一大促客服智能化保障复盘', required: true },
      { key: 'results', label: '原定目标与实际达成结果', placeholder: '列出核心 KPI 达成情况…', multiline: true },
      { key: 'issues', label: '主要亮点与暴露的问题', placeholder: '列出做得好的地方和踩过的坑…', multiline: true },
    ],
    buildPrompt: (v) => ({
      title: `${v.topic || '项目'}复盘总结`,
      instruction: [
        `请按 GRAI（目标回顾-结果评估-原因分析-规律与改进）框架撰写项目复盘报告：《${v.topic || '项目复盘'}》。`,
        v.results ? `目标与实际结果：${v.results}` : '',
        v.issues ? `亮点与问题：${v.issues}` : '',
        '输出结构：1. 核心指标达成对比表；2. 关键亮点与可复用经验；3. 问题根因拆解；4. 下阶段改进措施与责任人清单表。',
      ].filter(Boolean).join('\n'),
    }),
  },
]

export function assembleTemplatePrompt(
  template: OfficeDraftTemplate,
  values: Record<string, string>,
  persona?: OfficePersonaProfile | null,
): { title: string; prompt: string } {
  const { title, instruction } = template.buildPrompt(values)
  const personaPrefix = persona ? buildPersonaPromptBlock(persona) : ''
  const prompt = personaPrefix ? `${personaPrefix}\n\n${instruction}` : instruction
  return { title, prompt }
}
