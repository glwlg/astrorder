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
      '1. 加载已安装的 astrorder-sessions 与 astrorder-swarm 技能，通过技能目录内的 astrorder-agent CLI 调度星序。',
      '2. 使用 sessions spawn --operation-id 创建真正的委派伴星；CLI 从当前运行时读取自身会话身份，服务端建立上下级关系，不手填或猜测自身 sessionid。',
      '3. 按任务需要创建伴星并读取其实际最终结果；创建请求已受理不代表任务完成。跨机器任务使用目标机器上的工作区路径。',
      '4. 共享契约与状态通过 invoke blackboard.set 发布；阶段依赖通过 invoke swarm.milestone.declare / swarm.milestone.resolve 协调。复杂 JSON 写入 UTF-8 文件并使用 --input-file 传入。',
    )
  }
  if (blackboard) {
    const isArch = text.includes('[架构规范]')
    const isChecklist = text.includes('[任务清单]')
    const isExport = text.includes('[固化沉淀]')

    directives.push(
      '【星序 · 作战黑板同步指令】',
      '加载 astrorder-blackboard 技能，使用技能目录内的 astrorder-agent CLI：invoke blackboard.set（写入）或 invoke blackboard.get（读取）。',
      sessionKey ? `当前黑板作用域为“${sessionKey}”；个人会话身份由 CLI 自动解析，群聊或星图作用域通过 session_key 显式传入。` : '个人会话身份由 CLI 自动解析，不以最近或唯一活跃会话推断归属。',
      '复杂 JSON 通过 --input-file 传入 UTF-8 文件；直接使用 CLI，不通过读写源码或数据库模拟黑板操作。',
      isArch
        ? '请在 value 中明确包含 {"component": "ArchitectureFlow", ...} 或 {"component": "ApiEndpointsCard", ...}，将技术架构规范化发布到黑板。'
        : isChecklist
        ? '请在 value 中明确包含 {"component": "StepTimeline", "title": "任务名称", "steps": [{"title": "步骤1", "status": "completed"|"pending"}]} 或 {"component": "Checklist", "items": [{"label": "检查项", "checked": true|false}]} 规范化发布到黑板。'
        : isExport
        ? '请调用 invoke blackboard.get 读取当前会话或群聊黑板的核心数据，并将其以标准 Markdown 报告格式固化沉淀为工作区文档（如 docs/specs/blackboard_summary.md）。'
        : '黑板内容请提供有语义的 key（如 task_spec, api_design, test_report 等），并在 value 中显式声明 component 属性（如 StepTimeline, Checklist, DataTable, MetricGrid），以便呈现精美的 Generative UI。',
    )
  }
  if (browser) {
    directives.push(
      '【星序 · 浏览器自动执行指令】',
      '加载 astrorder-browser 技能，使用技能目录内的 astrorder-agent CLI：invoke browser.run --input-file <任务.json> 完成浏览器任务。',
      '需要检查 DOM、控制台、网络、性能或调试页面时，使用 invoke browser.cdp --input-file <请求.json>；复杂多行 JavaScript 使用 browser eval --script-file <脚本.js>。',
      sessionKey?.startsWith('group::') || sessionKey?.startsWith('swarm::')
        ? `浏览器操作归属当前共享作用域“${sessionKey}”；在任务和 CDP 请求 JSON 中显式传入 session_key，browser eval / screenshot 使用 --session-key 指定该作用域。`
        : sessionKey ? `浏览器操作归属当前会话“${sessionKey}”，CLI 从运行时精确身份自动绑定 session_key。` : '浏览器 session_key 由 CLI 从运行时精确身份自动绑定，不猜测其他活跃会话。',
      '通过星序 CLI 操作当前会话的浏览器标签页。',
      '从用户要求中整理 url、goal、需要填写的 inputs 和必要的 max_steps；以页面中可见的完成结果作为 goal 的停止条件。',
      '如需调用多个网站，必须比较所有成功结果并说明来源与差异，不得直接用最后一次结果覆盖更早的有效结果。',
    )
  }
  return [...directives.filter(Boolean), '----------------------------------------', prompt].join('\n')
}
