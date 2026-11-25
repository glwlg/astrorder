import type { SessionCheckpoint } from '../../../../api/client'
import { mapCheckpointsToMessages } from '../../../../domain/turnCheckpoints'
import type { Message, Task } from '../../../../domain/types'
import {
  cleanThinkingHeadline,
  describeTool,
  extractToolArgs,
  fileChangeDiffs,
  unwrapCommand,
} from '../../../chat/toolPresentation'

export type StepKind = 'user_prompt' | 'thinking' | 'planning' | 'tool_call' | 'subagent' | 'completed' | 'failed'
export type StepStatus = 'pending' | 'running' | 'completed' | 'failed'

export interface AgentGraphNode {
  id: string
  title: string
  kind: StepKind
  status: StepStatus
  detail?: string
  toolName?: string
  toolPayload?: unknown
  timestamp?: string
  rawMessage?: Message
  rawTask?: Task
}

export type FileTouchAction = 'created' | 'modified' | 'read'

export interface TurnFileTouch {
  path: string
  fileName: string
  displayPath: string
  action: FileTouchAction
  editCount: number
  readCount: number
  addedLines: number
  removedLines: number
  lastMessageId?: string
}

export interface SessionFileImpact extends TurnFileTouch {
  turns: number[]
  lastTurn: number
}

export type VerifyCategory = 'test' | 'build' | 'lint' | 'git' | 'shell' | 'tool_error'

export interface VerificationEntry {
  id: string
  turnIndex: number
  turnTitle: string
  command: string
  summary?: string
  category: VerifyCategory
  status: 'running' | 'passed' | 'failed'
  recovered: boolean
  output: string
  errorExcerpt?: string
  durationSec: number | null
  timestamp?: string
  messageId?: string
}

export interface FailureDiagnostic {
  id: string
  headline: string
  commandOrTool: string
  resolved: boolean
  resolutionNote?: string
  messageId?: string
  timestamp?: string
}

export type RawStepPhase = 'research' | 'decision' | 'edit' | 'verify' | 'subagent' | 'other'

export interface RawStepEntry {
  id: string
  phase: RawStepPhase
  title: string
  toolName?: string
  status: StepStatus
  timestamp?: string
  detail?: string
  messageId?: string
  toolPayload?: unknown
}

export type TurnOutcome = 'running' | 'passed_clean' | 'recovered' | 'has_errors' | 'completed'

export interface ExecutionTurn {
  index: number
  id: string
  title: string
  userPrompt?: string
  anchorMessageId?: string
  finalMessageId?: string
  startedAt?: string
  endedAt?: string
  durationSec: number | null
  outcome: TurnOutcome
  phases: {
    research: number
    decision: number
    editFiles: number
    verifyTotal: number
    verifyPassed: number
    verifyFailed: number
    subagent: number
  }
  decisions: string[]
  files: TurnFileTouch[]
  verifications: VerificationEntry[]
  diagnostics: FailureDiagnostic[]
  checkpoint: SessionCheckpoint | null
  rawSteps: RawStepEntry[]
  addedLines: number
  removedLines: number
}

export interface ExecutionHubSummary {
  turnCount: number
  runningTurns: number
  modifiedFileCount: number
  createdFileCount: number
  readOnlyFileCount: number
  totalTouchedFiles: number
  verifyPassed: number
  verifyFailed: number
  unresolvedErrorCount: number
  checkpointCount: number
  subagentCount: number
  totalRawSteps: number
}

export interface ExecutionHubData {
  turns: ExecutionTurn[]
  files: SessionFileImpact[]
  verifications: VerificationEntry[]
  subagents: Task[]
  summary: ExecutionHubSummary
}

function toolStatus(message: Message): StepStatus {
  const status = message.tool?.status
  if (status === 'running' || status === 'pending') return 'running'
  if (status === 'failed' || status === 'error') return 'failed'
  return 'completed'
}

function taskStatus(task: Task): StepStatus {
  if (task.status === 'running' || task.status === 'waiting_approval') return 'running'
  if (task.status === 'completed') return 'completed'
  if (task.status === 'failed' || task.status === 'cancelled') return 'failed'
  return 'pending'
}

export function parseNodesFromSession(messages: Message[], tasks: Task[]): AgentGraphNode[] {
  const nodes: AgentGraphNode[] = []

  for (const msg of messages) {
    const text = (msg.text || '').trim()

    if (msg.role === 'user' && msg.kind === 'message' && text) {
      nodes.push({
        id: `user-${msg.id}`,
        title: '用户目标指令',
        kind: 'user_prompt',
        status: 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.kind === 'thinking') {
      const desc = describeTool(msg)
      nodes.push({
        id: `think-${msg.id}`,
        title: desc.fullTitle,
        kind: 'thinking',
        status: desc.isRunning ? 'running' : desc.isFailed ? 'failed' : 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.kind === 'tool' || msg.tool) {
      const desc = describeTool(msg)
      const name = String(msg.tool?.name || 'tool')
      nodes.push({
        id: `tool-${msg.id}`,
        title: desc.fullTitle,
        kind: 'tool_call',
        status: toolStatus(msg),
        detail: text || desc.target,
        toolName: name,
        toolPayload: msg.tool,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.role === 'assistant' && text) {
      nodes.push({
        id: `asst-${msg.id}`,
        title: '阶段产出与决策',
        kind: 'completed',
        status: 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
    }
  }

  for (const task of tasks) {
    const isSubagent = task.kind === 'subagent'
    nodes.push({
      id: `task-${task.id}`,
      title: isSubagent ? `子代理: ${task.title}` : `任务: ${task.title}`,
      kind: isSubagent ? 'subagent' : 'planning',
      status: taskStatus(task),
      detail: task.command || undefined,
      rawTask: task,
      timestamp: task.updated_at || task.created_at,
    })
  }

  if (nodes.length === 0) {
    nodes.push({
      id: 'init-1',
      title: 'Agent 状态机就绪',
      kind: 'planning',
      status: 'pending',
      detail: '等待用户指令或正在初始化运行时上下文...',
    })
  }

  return nodes
}

// ============================================================================
// Execution & Decision Hub semantic aggregation engine
// ============================================================================

const EDIT_TOOL_NAMES = new Set([
  'fileChange',
  'patch',
  'write_file',
  'write_to_file',
  'edit',
  'edit_file',
  'replace_file_content',
  'multi_replace_file_content',
  'sed_file',
  'notebook_edit',
])

const CREATE_TOOL_NAMES = new Set(['write_file', 'write_to_file'])

const READ_TOOL_NAMES = new Set([
  'read_file',
  'fileRead',
  'view_file',
  'view_file_outline',
  'view_code_item',
  'read_resource',
  'read',
])

const RESEARCH_TOOL_NAMES = new Set([
  ...READ_TOOL_NAMES,
  'list_dir',
  'search_files',
  'fileSearch',
  'grep',
  'grep_search',
  'find_by_name',
  'search_web',
  'web_search',
  'read_url_content',
  'open_browser_url',
  'SearchContext',
])

const SHELL_TOOL_NAMES = new Set([
  'commandExecution',
  'terminal',
  'bash',
  'sh',
  'exec',
  'exec_command',
  'run_command',
  'command_status',
  'send_command_input',
])

const SUBAGENT_TOOL_NAMES = new Set(['invoke_subagent', 'send_message', 'swarm', 'define_subagent'])

function cleanPath(raw: string): string {
  const trimmed = raw.trim()
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      const parsed = JSON.parse(trimmed)
      if (typeof parsed === 'string') return parsed.trim()
    } catch {}
  }
  return trimmed
}

function firstString(args: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const val = args[key]
    if (typeof val === 'string' && val.trim() !== '') return val
  }
  return ''
}

function normalizeDisplayPath(fullPath: string, workspace?: string | null): string {
  const norm = fullPath.replace(/\\/g, '/')
  if (workspace) {
    const wsNorm = workspace.replace(/\\/g, '/').replace(/\/+$/, '')
    if (wsNorm && norm.toLowerCase().startsWith(wsNorm.toLowerCase() + '/')) {
      return norm.slice(wsNorm.length + 1)
    }
  }
  return norm
}

function baseFileName(filePath: string): string {
  return filePath.replace(/[\\/]+$/, '').split(/[/\\]/).pop() || filePath
}

function parseLineDeltas(message: Message): { added: number; removed: number } {
  const text = message.text || ''
  const summaryMatch = text.match(/\(\+(\d+)\s*\/\s*-(\d+)\s*lines?\)/i)
  if (summaryMatch) {
    return { added: Number(summaryMatch[1]) || 0, removed: Number(summaryMatch[2]) || 0 }
  }
  const diffs = fileChangeDiffs(message)
  if (!diffs.length) return { added: 0, removed: 0 }
  let added = 0
  let removed = 0
  for (const d of diffs) {
    for (const line of d.diff.split('\n')) {
      if (line.startsWith('+') && !line.startsWith('+++')) added++
      else if (line.startsWith('-') && !line.startsWith('---')) removed++
    }
  }
  return { added, removed }
}

function classifyShellCommand(cmd: string): VerifyCategory {
  const lowered = cmd.toLowerCase()
  if (/\b(go\s+test|vitest|jest|pytest|cargo\s+test|npm\s+(?:run\s+)?test|pnpm\s+test|unittest)\b/.test(lowered)) {
    return 'test'
  }
  if (/\b(vite\s+build|npm\s+run\s+build|pnpm\s+build|tsc\b|go\s+build|cargo\s+build|webpack|make\b|wails3?\s+build)\b/.test(lowered)) {
    return 'build'
  }
  if (/\b(eslint|npm\s+run\s+lint|golangci-lint|go\s+vet|cargo\s+clippy|ruff|flake8|prettier\s+--check)\b/.test(lowered)) {
    return 'lint'
  }
  if (/^\s*git\s/.test(lowered)) {
    return 'git'
  }
  return 'shell'
}

function isReadOnlyShellCommand(cmd: string): boolean {
  const trimmed = cmd.trim()
  return /^(?:rg|grep|find|ls|cat|head|tail|wc|pwd|which|git\s+(?:status|log|diff|show|branch))\b/i.test(trimmed)
}

function detectCommandFailure(message: Message, status: StepStatus): boolean {
  if (status === 'failed') return true
  if (status === 'running') return false
  const text = message.text || ''
  const exitMatch = text.match(/Command exited with code\s+(-?\d+)/i)
  if (exitMatch) {
    return Number(exitMatch[1]) !== 0
  }
  if (/\bimport cycle not allowed\b/i.test(text)) return true
  if (/(?:^|\n)(?:--- FAIL:|FAIL\t|FAIL\s+\S+\.(?:test|spec)\.|error TS\d+:|panic:)/m.test(text)) {
    return true
  }
  return false
}

function extractErrorExcerpt(rawText: string): string {
  if (!rawText) return ''
  const lines = rawText
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => {
      if (!line) return false
      if (/^(?:Created At|Completed At|Command output|Exit code|Command exited with code)\b/i.test(line)) return false
      return true
    })
  const priorityLine = lines.find((line) =>
    /(?:import cycle|error\b|failed\b|fail\b|cannot\b|undefined\b|panic:|exception|syntaxerror|typeerror|ts\d+:|not found|permission denied|refusing to)/i.test(line),
  )
  const chosen = priorityLine || lines[0] || ''
  return chosen.length > 180 ? `${chosen.slice(0, 177)}...` : chosen
}

function cleanUserTitle(raw: string): string {
  const cleaned = raw
    .replace(/^\/(?:plan|goal|compact|boost|grill-me|teamwork)\s*/i, '')
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .find((line) => line.length > 0) || raw.trim()
  return cleaned.length > 76 ? `${cleaned.slice(0, 73)}...` : cleaned
}

const BOILERPLATE_DECISION_RE = /^(?:let me|let's|now let's|i'll|i will|first,? let|good!|great!|awesome!|ok,? |okay,? |好的|先看|现在我|接下来我|我先|让我先)/i

function extractDecisionSnippets(text: string, maxCount: number): string[] {
  if (!text) return []
  const stripped = text.replace(/```[\s\S]*?```/g, '')
  const results: string[] = []
  const seen = new Set<string>()
  const pushClean = (candidate: string) => {
    const clean = candidate
      .replace(/^\s*(?:#+\s*|[-*+]\s+|\d+\.\s+|>\s*)+/, '')
      .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
      .replace(/\*\*([^*]+)\*\*/g, '$1')
      .replace(/`([^`]+)`/g, '$1')
      .replace(/\s+/g, ' ')
      .trim()
    if (clean.length < 10) return
    if (BOILERPLATE_DECISION_RE.test(clean)) return
    const capped = clean.length > 124 ? `${clean.slice(0, 121)}...` : clean
    const key = capped.toLowerCase()
    if (seen.has(key)) return
    seen.add(key)
    results.push(capped)
  }

  for (const line of stripped.split('\n')) {
    if (results.length >= maxCount) break
    const trimmed = line.trim()
    if (!trimmed || /^\|.*\|$/.test(trimmed) || /^[-=]{3,}$/.test(trimmed)) continue
    if (/^(?:[-*+]\s+|\d+\.\s+)/.test(trimmed)) {
      pushClean(trimmed)
    }
  }
  if (results.length < maxCount) {
    for (const line of stripped.split('\n')) {
      if (results.length >= maxCount) break
      const trimmed = line.trim()
      if (!trimmed || /^\|.*\|$/.test(trimmed) || /^#{1,6}\s/.test(trimmed)) continue
      pushClean(trimmed)
    }
  }
  return results
}

function parseTimeMs(iso?: string | null): number | null {
  if (!iso) return null
  const ms = Date.parse(iso)
  return Number.isFinite(ms) ? ms : null
}

function durationSeconds(startIso?: string | null, endIso?: string | null): number | null {
  const s = parseTimeMs(startIso)
  const e = parseTimeMs(endIso)
  if (s === null || e === null || e < s) return null
  return Math.round((e - s) / 1000)
}

function mergeFileTouch(
  map: Map<string, TurnFileTouch>,
  rawPath: string,
  action: FileTouchAction,
  workspace: string | null | undefined,
  deltas: { added: number; removed: number },
  messageId?: string,
) {
  const cleaned = cleanPath(rawPath)
  if (!cleaned) return
  const key = cleaned.replace(/\\/g, '/').toLowerCase()
  const existing = map.get(key)
  if (!existing) {
    map.set(key, {
      path: cleaned,
      fileName: baseFileName(cleaned),
      displayPath: normalizeDisplayPath(cleaned, workspace),
      action,
      editCount: action === 'read' ? 0 : 1,
      readCount: action === 'read' ? 1 : 0,
      addedLines: deltas.added,
      removedLines: deltas.removed,
      lastMessageId: messageId,
    })
    return
  }
  if (action === 'created') {
    existing.action = 'created'
    existing.editCount += 1
  } else if (action === 'modified') {
    if (existing.action === 'read') existing.action = 'modified'
    existing.editCount += 1
  } else {
    existing.readCount += 1
  }
  existing.addedLines += deltas.added
  existing.removedLines += deltas.removed
  if (messageId) existing.lastMessageId = messageId
}

export function buildExecutionHubData(
  messages: Message[],
  tasks: Task[],
  checkpoints: SessionCheckpoint[] = [],
  workspace?: string | null,
): ExecutionHubData {
  const checkpointMap = mapCheckpointsToMessages(messages, checkpoints)

  type TurnBucket = {
    index: number
    userMessage?: Message
    messages: Message[]
  }
  const buckets: TurnBucket[] = []
  let current: TurnBucket | null = null

  for (const msg of messages) {
    const text = (msg.text || '').trim()
    if (msg.role === 'user' && msg.kind === 'message' && text) {
      current = { index: buckets.length + 1, userMessage: msg, messages: [msg] }
      buckets.push(current)
      continue
    }
    if (msg.role === 'system' && msg.kind !== 'tool' && !msg.tool) continue
    if (!current) {
      current = { index: buckets.length + 1, messages: [] }
      buckets.push(current)
    }
    current.messages.push(msg)
  }

  const globalFiles = new Map<string, SessionFileImpact>()
  const globalVerifications: VerificationEntry[] = []
  const turns: ExecutionTurn[] = []

  for (const bucket of buckets) {
    const turnTitle = bucket.userMessage
      ? cleanUserTitle(bucket.userMessage.text || '')
      : '会话执行阶段'
    const turnFiles = new Map<string, TurnFileTouch>()
    const turnVerifications: VerificationEntry[] = []
    const rawSteps: RawStepEntry[] = []
    const thinkingTexts: string[] = []
    let finalAssistantMsg: Message | null = null
    let researchCount = 0
    let decisionCount = 0
    let subagentCount = 0
    let hasRunning = false

    const pendingFailures: Array<{
      id: string
      commandOrTool: string
      category?: VerifyCategory
      headline: string
      messageId: string
      timestamp?: string
      resolved: boolean
      resolutionNote?: string
      verificationRef?: VerificationEntry
    }> = []

    for (const msg of bucket.messages) {
      if (msg === bucket.userMessage) continue
      const text = (msg.text || '').trim()

      if (msg.kind === 'thinking') {
        decisionCount += 1
        const desc = describeTool(msg)
        const status: StepStatus = desc.isRunning ? 'running' : desc.isFailed ? 'failed' : 'completed'
        if (status === 'running') hasRunning = true
        if (text) {
          thinkingTexts.push(text)
          // Attach thinking explanation as resolution note for unresolved failure in this turn
          const latestUnresolved = [...pendingFailures].reverse().find((f) => !f.resolved && !f.resolutionNote)
          if (latestUnresolved) {
            const headline = cleanThinkingHeadline(text)
            if (headline && !BOILERPLATE_DECISION_RE.test(headline)) {
              latestUnresolved.resolutionNote = headline.length > 130 ? `${headline.slice(0, 127)}...` : headline
            }
          }
        }
        rawSteps.push({
          id: `think-${msg.id}`,
          phase: 'decision',
          title: desc.fullTitle,
          status,
          timestamp: msg.created_at,
          detail: text,
          messageId: msg.id,
        })
        continue
      }

      if (msg.kind === 'tool' || msg.tool) {
        const desc = describeTool(msg)
        const name = String(msg.tool?.name || 'tool')
        const args = extractToolArgs(msg.tool)
        const baseStatus = toolStatus(msg)
        if (baseStatus === 'running') hasRunning = true

        // 1. Subagent calls
        if (SUBAGENT_TOOL_NAMES.has(name)) {
          subagentCount += 1
          rawSteps.push({
            id: `tool-${msg.id}`,
            phase: 'subagent',
            title: desc.fullTitle,
            toolName: name,
            status: baseStatus,
            timestamp: msg.created_at,
            detail: text || desc.target,
            messageId: msg.id,
            toolPayload: msg.tool,
          })
          continue
        }

        // 2. File edits / writes
        if (EDIT_TOOL_NAMES.has(name) || Array.isArray(args.changes)) {
          const deltas = parseLineDeltas(msg)
          const isCreate =
            CREATE_TOOL_NAMES.has(name) &&
            (args.Overwrite === false || /created file/i.test(text) || !args.Overwrite)

          if (Array.isArray(args.changes)) {
            const changeList = args.changes as Array<{ path?: unknown }>
            const perFileDelta = changeList.length === 1 ? deltas : { added: 0, removed: 0 }
            for (const ch of changeList) {
              if (ch && typeof ch.path === 'string' && ch.path.trim()) {
                mergeFileTouch(turnFiles, ch.path, 'modified', workspace, perFileDelta, msg.id)
              }
            }
          } else {
            const pathVal = firstString(args, 'path', 'Path', 'TargetFile', 'AbsolutePath', 'file_path', 'file')
            if (pathVal) {
              mergeFileTouch(turnFiles, pathVal, isCreate ? 'created' : 'modified', workspace, deltas, msg.id)
            }
          }

          if (baseStatus === 'failed') {
            const excerpt = extractErrorExcerpt(text) || `${desc.fullTitle} 执行失败`
            pendingFailures.push({
              id: `diag-${msg.id}`,
              commandOrTool: desc.fullTitle,
              headline: excerpt,
              messageId: msg.id,
              timestamp: msg.created_at,
              resolved: false,
            })
            const errEntry: VerificationEntry = {
              id: `ver-${msg.id}`,
              turnIndex: bucket.index,
              turnTitle,
              command: desc.fullTitle,
              category: 'tool_error',
              status: 'failed',
              recovered: false,
              output: text,
              errorExcerpt: excerpt,
              durationSec: durationSeconds(msg.created_at, msg.completed_at),
              timestamp: msg.created_at,
              messageId: msg.id,
            }
            turnVerifications.push(errEntry)
            globalVerifications.push(errEntry)
          }

          rawSteps.push({
            id: `tool-${msg.id}`,
            phase: 'edit',
            title: desc.fullTitle,
            toolName: name,
            status: baseStatus,
            timestamp: msg.created_at,
            detail: text || desc.target,
            messageId: msg.id,
            toolPayload: msg.tool,
          })
          continue
        }

        // 3. Shell / verification commands
        const rawCmd = firstString(args, 'command', 'cmd', 'CommandLine', 'Command')
        if (SHELL_TOOL_NAMES.has(name) || Boolean(rawCmd)) {
          const cmd = unwrapCommand(rawCmd) || firstString(args, 'summary', 'toolSummary', 'Summary') || name
          const summary = firstString(args, 'toolSummary', 'summary', 'Summary') || undefined
          const failed = detectCommandFailure(msg, baseStatus)
          const effectiveStatus: StepStatus = baseStatus === 'running' ? 'running' : failed ? 'failed' : 'completed'

          if (isReadOnlyShellCommand(cmd) && !failed) {
            researchCount += 1
            rawSteps.push({
              id: `tool-${msg.id}`,
              phase: 'research',
              title: desc.fullTitle,
              toolName: name,
              status: effectiveStatus,
              timestamp: msg.created_at,
              detail: text || cmd,
              messageId: msg.id,
              toolPayload: msg.tool,
            })
            continue
          }

          const category = classifyShellCommand(cmd)
          const excerpt = failed ? extractErrorExcerpt(text) : undefined
          const verifyEntry: VerificationEntry = {
            id: `ver-${msg.id}`,
            turnIndex: bucket.index,
            turnTitle,
            command: cmd,
            summary: summary && summary !== cmd ? summary : undefined,
            category,
            status: effectiveStatus === 'running' ? 'running' : failed ? 'failed' : 'passed',
            recovered: false,
            output: text,
            errorExcerpt: excerpt || undefined,
            durationSec: durationSeconds(msg.created_at, msg.completed_at),
            timestamp: msg.created_at,
            messageId: msg.id,
          }
          turnVerifications.push(verifyEntry)
          globalVerifications.push(verifyEntry)

          if (failed) {
            pendingFailures.push({
              id: `diag-${msg.id}`,
              commandOrTool: cmd.length > 72 ? `${cmd.slice(0, 69)}...` : cmd,
              category,
              headline: excerpt || '命令执行返回非零退出码',
              messageId: msg.id,
              timestamp: msg.created_at,
              resolved: false,
              verificationRef: verifyEntry,
            })
          } else if (effectiveStatus === 'completed') {
            // Mark matching or earlier failures in this turn as recovered!
            for (const f of pendingFailures) {
              if (!f.resolved && (!f.category || f.category === category || category === 'test' || category === 'build')) {
                f.resolved = true
                if (f.verificationRef) f.verificationRef.recovered = true
              }
            }
          }

          rawSteps.push({
            id: `tool-${msg.id}`,
            phase: 'verify',
            title: desc.fullTitle,
            toolName: name,
            status: effectiveStatus,
            timestamp: msg.created_at,
            detail: text || cmd,
            messageId: msg.id,
            toolPayload: msg.tool,
          })
          continue
        }

        // 4. Read / search / research tools
        if (RESEARCH_TOOL_NAMES.has(name)) {
          researchCount += 1
          if (READ_TOOL_NAMES.has(name)) {
            const pathVal = firstString(args, 'path', 'Path', 'AbsolutePath', 'File', 'TargetFile')
            if (pathVal) {
              mergeFileTouch(turnFiles, pathVal, 'read', workspace, { added: 0, removed: 0 }, msg.id)
            }
          }
          if (baseStatus === 'failed') {
            const excerpt = extractErrorExcerpt(text) || `${desc.fullTitle} 失败`
            pendingFailures.push({
              id: `diag-${msg.id}`,
              commandOrTool: desc.fullTitle,
              headline: excerpt,
              messageId: msg.id,
              timestamp: msg.created_at,
              resolved: false,
            })
          }
          rawSteps.push({
            id: `tool-${msg.id}`,
            phase: 'research',
            title: desc.fullTitle,
            toolName: name,
            status: baseStatus,
            timestamp: msg.created_at,
            detail: text || desc.target,
            messageId: msg.id,
            toolPayload: msg.tool,
          })
          continue
        }

        // 5. Other tool calls (MCP, browser, etc.)
        if (baseStatus === 'failed') {
          const excerpt = extractErrorExcerpt(text) || `${desc.fullTitle} 执行失败`
          pendingFailures.push({
            id: `diag-${msg.id}`,
            commandOrTool: desc.fullTitle,
            headline: excerpt,
            messageId: msg.id,
            timestamp: msg.created_at,
            resolved: false,
          })
          const errEntry: VerificationEntry = {
            id: `ver-${msg.id}`,
            turnIndex: bucket.index,
            turnTitle,
            command: desc.fullTitle,
            category: 'tool_error',
            status: 'failed',
            recovered: false,
            output: text,
            errorExcerpt: excerpt,
            durationSec: durationSeconds(msg.created_at, msg.completed_at),
            timestamp: msg.created_at,
            messageId: msg.id,
          }
          turnVerifications.push(errEntry)
          globalVerifications.push(errEntry)
        }
        rawSteps.push({
          id: `tool-${msg.id}`,
          phase: 'other',
          title: desc.fullTitle,
          toolName: name,
          status: baseStatus,
          timestamp: msg.created_at,
          detail: text || desc.target,
          messageId: msg.id,
          toolPayload: msg.tool,
        })
        continue
      }

      if (msg.role === 'assistant' && text) {
        finalAssistantMsg = msg
        rawSteps.push({
          id: `asst-${msg.id}`,
          phase: 'decision',
          title: '阶段回复与总结',
          status: 'completed',
          timestamp: msg.created_at,
          detail: text,
          messageId: msg.id,
        })
      }
    }

    // Build decision highlights from assistant final reply + high-signal thinking
    const decisions: string[] = []
    if (finalAssistantMsg?.text) {
      decisions.push(...extractDecisionSnippets(finalAssistantMsg.text, 2))
    }
    if (decisions.length < 2 && thinkingTexts.length > 0) {
      for (const t of thinkingTexts.slice(-3)) {
        for (const snippet of extractDecisionSnippets(t, 2 - decisions.length)) {
          if (!decisions.includes(snippet)) decisions.push(snippet)
        }
        if (decisions.length >= 2) break
      }
    }
    if (decisions.length === 0 && finalAssistantMsg?.text) {
      const fallback = cleanThinkingHeadline(finalAssistantMsg.text)
      if (fallback) decisions.push(fallback.length > 124 ? `${fallback.slice(0, 121)}...` : fallback)
    }

    // Attach resolution note fallback from final reply if resolved
    for (const f of pendingFailures) {
      if (f.resolved && !f.resolutionNote && decisions[0]) {
        f.resolutionNote = decisions[0]
      }
    }

    // Merge turn files into global impact map
    const turnFileList = Array.from(turnFiles.values()).sort((a, b) => {
      const rank = { created: 0, modified: 1, read: 2 }
      return rank[a.action] - rank[b.action] || b.editCount - a.editCount || a.fileName.localeCompare(b.fileName)
    })

    let turnAdded = 0
    let turnRemoved = 0
    for (const file of turnFileList) {
      turnAdded += file.addedLines
      turnRemoved += file.removedLines
      const key = file.path.replace(/\\/g, '/').toLowerCase()
      const existing = globalFiles.get(key)
      if (!existing) {
        globalFiles.set(key, {
          ...file,
          turns: [bucket.index],
          lastTurn: bucket.index,
        })
      } else {
        if (file.action === 'created') existing.action = 'created'
        else if (file.action === 'modified' && existing.action === 'read') existing.action = 'modified'
        existing.editCount += file.editCount
        existing.readCount += file.readCount
        existing.addedLines += file.addedLines
        existing.removedLines += file.removedLines
        if (!existing.turns.includes(bucket.index)) existing.turns.push(bucket.index)
        existing.lastTurn = Math.max(existing.lastTurn, bucket.index)
        if (file.lastMessageId) existing.lastMessageId = file.lastMessageId
      }
    }

    const editedFilesInTurn = turnFileList.filter((f) => f.action !== 'read')
    const verifyPassed = turnVerifications.filter((v) => v.status === 'passed').length
    const verifyFailed = turnVerifications.filter((v) => v.status === 'failed').length
    const unresolvedFailures = pendingFailures.filter((f) => !f.resolved)
    const checkpoint = finalAssistantMsg ? checkpointMap.get(finalAssistantMsg.id) ?? null : null

    if (checkpoint && turnAdded === 0 && turnRemoved === 0 && (checkpoint.insertions > 0 || checkpoint.deletions > 0)) {
      turnAdded = checkpoint.insertions
      turnRemoved = checkpoint.deletions
    }

    let outcome: TurnOutcome = 'completed'
    if (hasRunning) {
      outcome = 'running'
    } else if (unresolvedFailures.length > 0) {
      outcome = 'has_errors'
    } else if (pendingFailures.length > 0) {
      outcome = 'recovered'
    } else if (verifyPassed > 0) {
      outcome = 'passed_clean'
    }

    const firstMsg = bucket.messages[0]
    const lastMsg = bucket.messages[bucket.messages.length - 1]
    const startedAt = firstMsg?.created_at
    const endedAt = lastMsg?.completed_at || lastMsg?.created_at

    turns.push({
      index: bucket.index,
      id: bucket.userMessage ? `turn-${bucket.userMessage.id}` : `turn-auto-${bucket.index}`,
      title: turnTitle,
      userPrompt: bucket.userMessage?.text?.trim(),
      anchorMessageId: bucket.userMessage?.id || finalAssistantMsg?.id || firstMsg?.id,
      finalMessageId: finalAssistantMsg?.id,
      startedAt,
      endedAt,
      durationSec: durationSeconds(startedAt, endedAt),
      outcome,
      phases: {
        research: researchCount,
        decision: decisionCount + (finalAssistantMsg ? 1 : 0),
        editFiles: editedFilesInTurn.length,
        verifyTotal: turnVerifications.length,
        verifyPassed,
        verifyFailed,
        subagent: subagentCount,
      },
      decisions,
      files: turnFileList,
      verifications: turnVerifications,
      diagnostics: pendingFailures.map((f) => ({
        id: f.id,
        headline: f.headline,
        commandOrTool: f.commandOrTool,
        resolved: f.resolved,
        resolutionNote: f.resolutionNote,
        messageId: f.messageId,
        timestamp: f.timestamp,
      })),
      checkpoint,
      rawSteps,
      addedLines: turnAdded,
      removedLines: turnRemoved,
    })
  }

  const allFiles = Array.from(globalFiles.values()).sort((a, b) => {
    const rank = { created: 0, modified: 1, read: 2 }
    return (
      rank[a.action] - rank[b.action] ||
      b.lastTurn - a.lastTurn ||
      b.editCount - a.editCount ||
      a.fileName.localeCompare(b.fileName)
    )
  })

  const subagents = tasks.filter((t) => t.kind === 'subagent')
  const meaningfulCheckpoints = checkpoints.filter((cp) => cp.kind !== 'auto_before')

  const summary: ExecutionHubSummary = {
    turnCount: turns.length,
    runningTurns: turns.filter((t) => t.outcome === 'running').length,
    modifiedFileCount: allFiles.filter((f) => f.action === 'modified').length,
    createdFileCount: allFiles.filter((f) => f.action === 'created').length,
    readOnlyFileCount: allFiles.filter((f) => f.action === 'read').length,
    totalTouchedFiles: allFiles.length,
    verifyPassed: globalVerifications.filter((v) => v.status === 'passed').length,
    verifyFailed: globalVerifications.filter((v) => v.status === 'failed').length,
    unresolvedErrorCount: turns.reduce((acc, t) => acc + t.diagnostics.filter((d) => !d.resolved).length, 0),
    checkpointCount: meaningfulCheckpoints.length,
    subagentCount: subagents.length,
    totalRawSteps: turns.reduce((acc, t) => acc + t.rawSteps.length, 0),
  }

  return {
    turns,
    files: allFiles,
    verifications: globalVerifications,
    subagents,
    summary,
  }
}
