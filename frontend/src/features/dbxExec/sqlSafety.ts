import type { DbxExecuteResult, DbxSqlClassification, DbxStatementResult } from '../../api/client'

export type SqlRiskLevel = 'read_only' | 'write' | 'destructive'

interface SqlToken {
  text: string
  upper: string
  kind: 'word' | 'semi' | 'lparen' | 'rparen' | 'other'
}

const DESTRUCTIVE_VERBS = new Set(['DROP', 'TRUNCATE', 'ALTER', 'CREATE', 'GRANT', 'REVOKE', 'RENAME'])
const TRANSACTION_VERBS = new Set(['BEGIN', 'START', 'COMMIT', 'ROLLBACK', 'SAVEPOINT', 'RELEASE', 'XA'])
const WRITE_VERBS = new Set([
  'INSERT',
  'MERGE',
  'REPLACE',
  'UPSERT',
  'COPY',
  'LOAD',
  'CALL',
  'EXEC',
  'EXECUTE',
  'DO',
  'SET',
  'RESET',
  'LOCK',
  'UNLOCK',
  'VACUUM',
  'ANALYZE',
  'ANALYSE',
  'REINDEX',
  'CLUSTER',
  'REFRESH',
  'PREPARE',
  'DEALLOCATE',
  'COMMENT',
  'ATTACH',
  'DETACH',
])
const READ_VERBS = new Set(['SELECT', 'SHOW', 'DESCRIBE', 'DESC', 'VALUES', 'TABLE'])

function isWordStart(ch: string): boolean {
  const c = ch.charCodeAt(0)
  return (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c === 95
}

function isWordContinue(ch: string): boolean {
  const c = ch.charCodeAt(0)
  return isWordStart(ch) || (c >= 48 && c <= 57)
}

function matchDollarTag(input: string, pos: number): string | null {
  if (pos + 1 >= input.length || input[pos] !== '$') return null
  if (input[pos + 1] === '$') return '$$'
  if (!isWordStart(input[pos + 1])) return null
  for (let i = pos + 2; i < input.length; i += 1) {
    const ch = input[i]
    if (ch === '$') return input.slice(pos, i + 1)
    if (!isWordContinue(ch)) return null
  }
  return null
}

function tokenizeSql(input: string): SqlToken[] {
  const tokens: SqlToken[] = []
  const n = input.length
  let i = 0

  while (i < n) {
    const ch = input[i]
    if (ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n' || ch === '\f') {
      i += 1
      continue
    }
    // Line comment: --
    if (ch === '-' && i + 1 < n && input[i + 1] === '-') {
      i += 2
      while (i < n && input[i] !== '\n') i += 1
      continue
    }
    // Block comment: /* ... */ (supports nesting)
    if (ch === '/' && i + 1 < n && input[i + 1] === '*') {
      let depth = 1
      i += 2
      while (i < n && depth > 0) {
        if (input[i] === '/' && i + 1 < n && input[i + 1] === '*') {
          depth += 1
          i += 2
          continue
        }
        if (input[i] === '*' && i + 1 < n && input[i + 1] === '/') {
          depth -= 1
          i += 2
          continue
        }
        i += 1
      }
      continue
    }
    // Single-quoted string
    if (ch === "'") {
      i += 1
      while (i < n) {
        if (input[i] === '\\' && i + 1 < n) {
          i += 2
          continue
        }
        if (input[i] === "'") {
          i += 1
          if (i < n && input[i] === "'") {
            i += 1
            continue
          }
          break
        }
        i += 1
      }
      tokens.push({ text: '', upper: '', kind: 'other' })
      continue
    }
    // Double-quoted identifier
    if (ch === '"') {
      i += 1
      while (i < n) {
        if (input[i] === '"') {
          i += 1
          if (i < n && input[i] === '"') {
            i += 1
            continue
          }
          break
        }
        i += 1
      }
      tokens.push({ text: '', upper: '', kind: 'other' })
      continue
    }
    // Backtick identifier
    if (ch === '`') {
      i += 1
      while (i < n) {
        if (input[i] === '`') {
          i += 1
          if (i < n && input[i] === '`') {
            i += 1
            continue
          }
          break
        }
        i += 1
      }
      tokens.push({ text: '', upper: '', kind: 'other' })
      continue
    }
    // PostgreSQL dollar-quoted string
    if (ch === '$') {
      const tag = matchDollarTag(input, i)
      if (tag) {
        i += tag.length
        const end = input.indexOf(tag, i)
        i = end >= 0 ? end + tag.length : n
        tokens.push({ text: '', upper: '', kind: 'other' })
        continue
      }
    }
    if (ch === ';') {
      tokens.push({ text: ';', upper: ';', kind: 'semi' })
      i += 1
      continue
    }
    if (ch === '(') {
      tokens.push({ text: '(', upper: '(', kind: 'lparen' })
      i += 1
      continue
    }
    if (ch === ')') {
      tokens.push({ text: ')', upper: ')', kind: 'rparen' })
      i += 1
      continue
    }
    if (isWordStart(ch)) {
      const start = i
      i += 1
      while (i < n && isWordContinue(input[i])) i += 1
      const word = input.slice(start, i)
      tokens.push({ text: word, upper: word.toUpperCase(), kind: 'word' })
      continue
    }
    i += 1
  }

  return tokens
}

function splitSqlStatements(sql: string): SqlToken[][] {
  const tokens = tokenizeSql(sql)
  const stmts: SqlToken[][] = []
  let cur: SqlToken[] = []
  let depth = 0
  for (const tok of tokens) {
    if (tok.kind === 'lparen') depth += 1
    else if (tok.kind === 'rparen' && depth > 0) depth -= 1
    if (tok.kind === 'semi' && depth === 0) {
      if (cur.length > 0) {
        stmts.push(cur)
        cur = []
      }
      continue
    }
    cur.push(tok)
  }
  if (cur.length > 0) stmts.push(cur)
  return stmts
}

function hasTopLevelWord(tokens: SqlToken[], target: string): boolean {
  let depth = 0
  for (const tok of tokens) {
    if (tok.kind === 'lparen') depth += 1
    else if (tok.kind === 'rparen' && depth > 0) depth -= 1
    else if (tok.kind === 'word' && depth === 0 && tok.upper === target) return true
  }
  return false
}

function containsAnyWord(tokens: SqlToken[], targets: string[]): boolean {
  for (const tok of tokens) {
    if (tok.kind === 'word' && targets.includes(tok.upper)) return true
  }
  return false
}

function hasRowLockClause(tokens: SqlToken[]): boolean {
  for (let i = 0; i < tokens.length; i += 1) {
    if (tokens[i].kind !== 'word' || tokens[i].upper !== 'FOR') continue
    for (let j = i + 1; j < tokens.length && j <= i + 4; j += 1) {
      if (tokens[j].kind !== 'word') break
      if (tokens[j].upper === 'UPDATE' || tokens[j].upper === 'SHARE') return true
      if (tokens[j].upper === 'NO' || tokens[j].upper === 'KEY') continue
      break
    }
  }
  return false
}

function classifySingleStatement(tokens: SqlToken[]): {
  verb: string
  risk: SqlRiskLevel
  warnings: string[]
} {
  const firstIdx = tokens.findIndex((tok) => tok.kind === 'word')
  if (firstIdx < 0) {
    return { verb: '', risk: 'write', warnings: ['未识别出有效 SQL 关键字'] }
  }
  const verb = tokens[firstIdx].upper
  const rest = tokens.slice(firstIdx + 1)

  if (DESTRUCTIVE_VERBS.has(verb)) {
    return { verb, risk: 'destructive', warnings: [`包含 DDL 或权限变更语句（${verb}）`] }
  }
  if (TRANSACTION_VERBS.has(verb)) {
    return { verb, risk: 'write', warnings: [`包含事务控制语句（${verb}）`] }
  }
  if (verb === 'DELETE' || verb === 'UPDATE') {
    if (!hasTopLevelWord(rest, 'WHERE')) {
      return { verb, risk: 'destructive', warnings: [`检测到缺少 WHERE 条件的 ${verb} 语句`] }
    }
    return { verb, risk: 'write', warnings: [`包含数据写入语句（${verb}）`] }
  }
  if (WRITE_VERBS.has(verb)) {
    return { verb, risk: 'write', warnings: [`包含写入或会话变更语句（${verb}）`] }
  }
  if (READ_VERBS.has(verb)) {
    if (verb === 'SELECT') {
      if (hasTopLevelWord(rest, 'INTO')) {
        return { verb, risk: 'write', warnings: ['SELECT ... INTO 会创建或写入目标表'] }
      }
      if (hasRowLockClause(rest)) {
        return { verb, risk: 'write', warnings: ['SELECT 包含行锁子句（FOR UPDATE / FOR SHARE）'] }
      }
    }
    return { verb, risk: 'read_only', warnings: [] }
  }
  if (verb === 'EXPLAIN') {
    if (containsAnyWord(rest, ['ANALYZE', 'ANALYSE'])) {
      const destructive = containsAnyWord(rest, ['DROP', 'TRUNCATE', 'ALTER', 'CREATE', 'GRANT', 'REVOKE'])
      return {
        verb,
        risk: destructive ? 'destructive' : 'write',
        warnings: ['EXPLAIN ANALYZE 会实际执行内部语句'],
      }
    }
    if (containsAnyWord(rest, ['INSERT', 'UPDATE', 'DELETE', 'MERGE', 'DROP', 'TRUNCATE', 'ALTER', 'CREATE'])) {
      return { verb, risk: 'write', warnings: ['EXPLAIN 目标包含写入或 DDL 语句'] }
    }
    return { verb, risk: 'read_only', warnings: [] }
  }
  if (verb === 'WITH') {
    if (containsAnyWord(rest, ['DROP', 'TRUNCATE', 'ALTER', 'CREATE', 'GRANT', 'REVOKE'])) {
      return { verb, risk: 'destructive', warnings: ['WITH 语句包含高危 DDL/权限变更'] }
    }
    if (containsAnyWord(rest, ['INSERT', 'UPDATE', 'DELETE', 'MERGE', 'REPLACE', 'INTO'])) {
      return { verb, risk: 'write', warnings: ['WITH 语句包含数据写入子句'] }
    }
    if (hasRowLockClause(rest)) {
      return { verb, risk: 'write', warnings: ['WITH 查询包含行锁子句（FOR UPDATE / FOR SHARE）'] }
    }
    if (containsAnyWord(rest, ['SELECT', 'VALUES', 'TABLE'])) {
      return { verb, risk: 'read_only', warnings: [] }
    }
    return { verb, risk: 'write', warnings: ['无法确认 WITH 语句为纯只读查询'] }
  }
  return { verb, risk: 'write', warnings: [`未归类为只读的语句（${verb}）`] }
}

export function classifySql(sql: string): DbxSqlClassification {
  const stmts = splitSqlStatements(sql)
  if (stmts.length === 0) {
    return {
      risk_level: 'write',
      statement_count: 0,
      primary_verb: '',
      warnings: ['SQL 内容为空'],
      read_only_allowed: false,
    }
  }

  let overall: SqlRiskLevel = 'read_only'
  let primaryVerb = ''
  const warnings: string[] = []
  const addWarning = (msg: string) => {
    if (msg && !warnings.includes(msg)) warnings.push(msg)
  }
  const promote = (next: SqlRiskLevel) => {
    if (next === 'destructive') overall = 'destructive'
    else if (next === 'write' && overall === 'read_only') overall = 'write'
  }

  if (stmts.length > 1) {
    addWarning(`多语句脚本（共 ${stmts.length} 条语句）`)
  }

  stmts.forEach((stmt, idx) => {
    const info = classifySingleStatement(stmt)
    if (idx === 0) primaryVerb = info.verb
    promote(info.risk)
    info.warnings.forEach(addWarning)
  })

  return {
    risk_level: overall,
    statement_count: stmts.length,
    primary_verb: primaryVerb,
    warnings,
    read_only_allowed: overall === 'read_only',
  }
}

export function requiresExecutionConfirmation(sql: string, _isProd = false): boolean {
  const classification = classifySql(sql)
  return !classification.read_only_allowed
}

export function formatCellDisplay(value: unknown, maxLen = 120): string {
  if (value === null || value === undefined) return 'NULL'
  let text: string
  if (typeof value === 'string') {
    text = value
  } else if (typeof value === 'number' || typeof value === 'boolean') {
    text = String(value)
  } else {
    try {
      text = JSON.stringify(value)
    } catch {
      text = String(value)
    }
  }
  const singleLine = text.replace(/\r?\n/g, ' ↵ ')
  if (singleLine.length > maxLen) {
    return `${singleLine.slice(0, maxLen)}…`
  }
  return singleLine
}

function escapeMarkdownCell(value: unknown): string {
  return formatCellDisplay(value, 120).replace(/\|/g, '\\|')
}

function formatStatementTable(stmt: DbxStatementResult, maxRows = 30): string {
  const lines: string[] = []
  if (stmt.error) {
    lines.push(`错误：\`${stmt.error}\``)
    return lines.join('\n')
  }
  const cols = stmt.columns || []
  const rows = stmt.rows || []
  if (cols.length === 0) {
    if (typeof stmt.affected_rows === 'number') {
      lines.push(`影响行数：${stmt.affected_rows}${stmt.elapsed_ms ? `（耗时 ${stmt.elapsed_ms} ms）` : ''}`)
    } else if (stmt.message) {
      lines.push(stmt.message)
    } else {
      lines.push(`执行完成${stmt.elapsed_ms ? `（耗时 ${stmt.elapsed_ms} ms）` : ''}`)
    }
    return lines.join('\n')
  }

  lines.push(`| ${cols.map((c) => escapeMarkdownCell(c.name)).join(' | ')} |`)
  lines.push(`| ${cols.map(() => '---').join(' | ')} |`)
  const shown = rows.slice(0, maxRows)
  for (const row of shown) {
    const cells = cols.map((_, idx) => escapeMarkdownCell(row[idx]))
    lines.push(`| ${cells.join(' | ')} |`)
  }
  if (rows.length > maxRows || stmt.truncated) {
    lines.push(`*（仅展示前 ${shown.length} 行，本次返回 ${rows.length} 行${stmt.truncated ? '，已截断' : ''}）*`)
  }
  return lines.join('\n')
}

export function formatSqlResultAsMarkdown(params: {
  result: DbxExecuteResult
  sourceLabel?: string
  sql?: string
}): string {
  const { result, sourceLabel, sql } = params
  const targetParts = [result.connection_name || result.connection_id]
  if (result.database) targetParts.push(result.database)
  if (result.schema) targetParts.push(result.schema)
  const targetStr = `${targetParts.join(' / ')}${result.is_prod ? ' [PROD]' : ''}`
  const statusStr = result.ok ? '执行成功' : '执行失败'

  const headerParts = [`SQL ${statusStr}（\`${targetStr}\`，耗时 ${result.total_elapsed_ms} ms）`]
  if (sourceLabel) {
    headerParts.push(`来源：\`${sourceLabel}\``)
  }

  const sections: string[] = [headerParts.join(' · ')]

  if (sql && !sourceLabel) {
    const compact = sql.trim()
    const snippet = compact.length > 260 ? `${compact.slice(0, 260)}\n-- …` : compact
    sections.push(`\`\`\`sql\n${snippet}\n\`\`\``)
  }

  if (!result.ok && result.error && (!result.results || result.results.length === 0)) {
    sections.push(`\`\`\`text\n${result.error}\n\`\`\``)
    return sections.join('\n\n')
  }

  const stmts = result.results || []
  if (stmts.length === 1) {
    sections.push(formatStatementTable(stmts[0]))
  } else if (stmts.length > 1) {
    stmts.forEach((stmt, idx) => {
      sections.push(`**语句 ${idx + 1}**\n${formatStatementTable(stmt, 15)}`)
    })
  } else if (result.ok) {
    sections.push('执行完成，无返回结果集。')
  }

  return sections.join('\n\n')
}

export function formatSqlResultAsJson(result: DbxExecuteResult): string {
  const payload = (result.results || []).map((stmt) => {
    const cols = stmt.columns || []
    const rows = (stmt.rows || []).map((row) => {
      const obj: Record<string, unknown> = {}
      cols.forEach((col, i) => {
        obj[col.name || `col_${i}`] = row[i]
      })
      return obj
    })
    return {
      index: stmt.index,
      row_count: stmt.row_count,
      affected_rows: stmt.affected_rows,
      elapsed_ms: stmt.elapsed_ms,
      truncated: stmt.truncated,
      error: stmt.error || undefined,
      rows: cols.length > 0 ? rows : undefined,
    }
  })
  return JSON.stringify(payload.length === 1 ? payload[0] : payload, null, 2)
}
