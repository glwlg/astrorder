const SWARM_MENTION = /(?:^|\s)@(群星|stars)(?=\s|$)/gi
const BROWSER_MENTION = /(?:^|\s)@(浏览器|browser)(?=\s|$)/gi
const BLACKBOARD_MENTION = /(?:^|\s)@(黑板|blackboard)(?=\s|$)/gi

export function expandSystemMentions(text: string, sessionKey?: string): string {
  const swarm = new RegExp(SWARM_MENTION.source, SWARM_MENTION.flags).test(text)
  const browser = new RegExp(BROWSER_MENTION.source, BROWSER_MENTION.flags).test(text)
  const blackboard = new RegExp(BLACKBOARD_MENTION.source, BLACKBOARD_MENTION.flags).test(text)
  if (!swarm && !browser && !blackboard) return text

  const prompt = text
    .replace(SWARM_MENTION, (match) => match.replace(/@(群星|stars)$/i, '【星序 · 群星多 Agent 协同作战指令】'))
    .replace(BROWSER_MENTION, (match) => match.replace(/@(浏览器|browser)$/i, '【星序 · 浏览器自动执行指令】'))
    .replace(BLACKBOARD_MENTION, (match) => match.replace(/@(黑板|blackboard)$/i, '【星序 · 作战黑板同步指令】'))
    .trim()
  const directives: string[] = []
  if (swarm) {
    directives.push(
      '【星序 · 群星多 Agent 协同作战指令】',
      '你当前担任本次任务的“主星 (Lead Star)”。请遵循星序协同规范执行：',
      '1. 必须直接通过原生工具调用发起 Astrorder MCP 工具调用 (如 mcp:astrorder.machines_dispatch / mcp:astrorder.sessions_create / mcp:astrorder.blackboard_set / mcp:astrorder.monitor_sessions_add / mcp:astrorder.plugins_open 等)；',
      '2. 严禁自己写 Python 脚本或敲终端命令行去模拟调用星序接口！直接发起原生 MCP 工具调用；',
      '3. 派生伴星时记得声明 parent_key (即当前会话 key) 以便星图拓扑正确连线，不要一个人在当前单个上下文中硬扛全流程，跨机器/跨环境派生伴星执行；',
      '4. 共享契约与状态发布至黑板 (mcp:astrorder.blackboard_set)。星序黑板配备了基于 json-render 的智能可视化引擎，请优先使用结构化对象发布；阶段依赖使用军令门禁 (mcp:astrorder.swarm_milestone_declare / resolve) 协调。',
    )
  }
  if (blackboard) {
    const isArch = text.includes('[架构规范]')
    const isChecklist = text.includes('[任务清单]')
    const isExport = text.includes('[固化沉淀]')

    directives.push(
      '【星序 · 作战黑板同步指令】',
      '必须直接调用 Astrorder MCP 工具 mcp:astrorder.blackboard_set（写入）或 mcp:astrorder.blackboard_get（读取）。',
      sessionKey ? `blackboard_set / blackboard_get 的 session_key 参数必须传入“${sessionKey}”。` : '',
      '严禁编写 Python 脚本、Bash 脚本或读写源码/数据库去模拟黑板操作，必须发起原生 MCP 工具调用。',
      isArch
        ? '请在 value 中明确包含 {"component": "ArchitectureFlow", ...} 或 {"component": "ApiEndpointsCard", ...}，将技术架构规范化发布到黑板。'
        : isChecklist
        ? '请在 value 中明确包含 {"component": "StepTimeline", "title": "任务名称", "steps": [{"title": "步骤1", "status": "completed"|"pending"}]} 或 {"component": "Checklist", "items": [{"label": "检查项", "checked": true|false}]} 规范化发布到黑板。'
        : isExport
        ? '请调用 blackboard_get 读取当前会话或群聊黑板的核心数据，并将其以标准 Markdown 报告格式固化沉淀为工作区文档（如 docs/specs/blackboard_summary.md）。'
        : '黑板内容请提供有语义的 key（如 task_spec, api_design, test_report 等），并在 value 中显式声明 component 属性（如 StepTimeline, Checklist, DataTable, MetricGrid），以便呈现精美的 Generative UI。',
    )
  }
  if (browser) {
    directives.push(
      '【星序 · 浏览器自动执行指令】',
      '必须使用 Astrorder MCP 工具 mcp:astrorder.browser_run 完成浏览器任务。',
      '需要检查 DOM、控制台、网络、性能或调试页面时，使用 mcp:astrorder.browser_cdp 操作当前会话的标签页。',
      sessionKey ? `browser_run 和 browser_cdp 的 session_key 必须传入“${sessionKey}”。` : '',
      '不要调用 Agent 自带的浏览器技能、Playwright、终端脚本或其他浏览器控制工具。',
      '从用户要求中整理 url、goal、需要填写的 inputs 和必要的 max_steps；以页面中可见的完成结果作为 goal 的停止条件。',
      '如需调用多个网站，必须比较所有成功结果并说明来源与差异，不得直接用最后一次结果覆盖更早的有效结果。',
    )
  }
  return [...directives.filter(Boolean), '----------------------------------------', prompt].join('\n')
}
