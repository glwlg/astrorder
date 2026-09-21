const SWARM_MENTION = /(?:^|\s)@(群星|stars)(?=\s|$)/gi
const BROWSER_MENTION = /(?:^|\s)@(浏览器|browser)(?=\s|$)/gi

export function expandSystemMentions(text: string): string {
  const swarm = new RegExp(SWARM_MENTION.source, SWARM_MENTION.flags).test(text)
  const browser = new RegExp(BROWSER_MENTION.source, BROWSER_MENTION.flags).test(text)
  if (!swarm && !browser) return text

  const prompt = text
    .replace(SWARM_MENTION, (match) => match.replace(/@(群星|stars)$/i, '【星序 · 群星多 Agent 协同作战指令】'))
    .replace(BROWSER_MENTION, (match) => match.replace(/@(浏览器|browser)$/i, '【星序 · 浏览器自动执行指令】'))
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
  if (browser) {
    directives.push(
      '【星序 · 浏览器自动执行指令】',
      '必须使用 Astrorder MCP 工具 mcp:astrorder.browser_run 完成浏览器任务。',
      '不要调用 Agent 自带的浏览器技能、Playwright、终端脚本或其他浏览器控制工具。',
      '从用户要求中整理 url、goal、需要填写的 inputs 和必要的 max_steps；以页面中可见的完成结果作为 goal 的停止条件。',
      '如需调用多个网站，必须比较所有成功结果并说明来源与差异，不得直接用最后一次结果覆盖更早的有效结果。',
    )
  }
  return [...directives, '----------------------------------------', prompt].join('\n')
}
