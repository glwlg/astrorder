import { isValidElement, type ReactNode } from 'react'

export type CalloutKind = 'note' | 'tip' | 'important' | 'warning' | 'caution'

export interface ParsedCallout {
  kind: CalloutKind
  label: string
  children: ReactNode
}

const CALLOUT_LABELS: Record<CalloutKind, string> = {
  note: '说明',
  tip: '提示',
  important: '重要',
  warning: '注意',
  caution: '警示',
}

const ALERT_RE = /^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n?/i

function stripAlertPrefix(node: ReactNode): { kind: CalloutKind; rest: ReactNode } | null {
  if (typeof node === 'string') {
    const match = node.match(ALERT_RE)
    if (!match) return null
    const kind = match[1].toLowerCase() as CalloutKind
    return { kind, rest: node.slice(match[0].length) }
  }
  if (Array.isArray(node)) {
    for (let i = 0; i < node.length; i++) {
      const item = node[i]
      if (typeof item === 'string' && !item.trim()) continue
      const found = stripAlertPrefix(item)
      if (!found) return null
      const next = [...node]
      if (found.rest === '' || found.rest === null) next.splice(i, 1)
      else next[i] = found.rest
      return { kind: found.kind, rest: next }
    }
    return null
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    const found = stripAlertPrefix(node.props.children)
    if (!found) return null
    return {
      kind: found.kind,
      rest: { ...node, props: { ...node.props, children: found.rest } },
    }
  }
  return null
}

/**
 * Detects GitHub-style alert blockquotes (`> [!TIP]`, `> [!WARNING]`, etc.)
 * and strips the marker cleanly while preserving all formatted React children.
 */
export function parseCalloutBlockquote(children: ReactNode): ParsedCallout | null {
  const found = stripAlertPrefix(children)
  if (!found) return null
  return {
    kind: found.kind,
    label: CALLOUT_LABELS[found.kind],
    children: found.rest,
  }
}

type TokenKind =
  | 'comment'
  | 'string'
  | 'number'
  | 'keyword'
  | 'type'
  | 'func'
  | 'prop'
  | 'add'
  | 'del'
  | 'meta'

interface TokenRule {
  kind: TokenKind
  regex: RegExp
}

const KEYWORDS_BY_FAMILY: Record<string, Set<string>> = {
  c_like: new Set([
    'async', 'await', 'break', 'case', 'catch', 'chan', 'class', 'const', 'continue',
    'default', 'defer', 'delete', 'do', 'else', 'enum', 'export', 'extends', 'fallthrough',
    'false', 'finally', 'fn', 'for', 'from', 'func', 'function', 'go', 'goto', 'if', 'impl',
    'import', 'in', 'instanceof', 'interface', 'let', 'loop', 'map', 'match', 'mod', 'mut',
    'new', 'nil', 'null', 'of', 'package', 'private', 'protected', 'pub', 'public', 'range',
    'readonly', 'return', 'select', 'static', 'struct', 'super', 'switch', 'this', 'throw',
    'trait', 'true', 'try', 'type', 'typeof', 'undefined', 'use', 'var', 'void', 'where',
    'while', 'yield',
  ]),
  python: new Set([
    'False', 'None', 'True', 'and', 'as', 'assert', 'async', 'await', 'break', 'class',
    'continue', 'def', 'del', 'elif', 'else', 'except', 'finally', 'for', 'from', 'global',
    'if', 'import', 'in', 'is', 'lambda', 'match', 'nonlocal', 'not', 'or', 'pass', 'raise',
    'return', 'try', 'while', 'with', 'yield',
  ]),
  shell: new Set([
    'case', 'do', 'done', 'elif', 'else', 'esac', 'export', 'fi', 'for', 'function', 'if',
    'in', 'local', 'readonly', 'return', 'set', 'then', 'unset', 'until', 'while',
  ]),
  sql: new Set([
    'ADD', 'ALL', 'ALTER', 'ALWAYS', 'ANALYZE', 'AND', 'AS', 'ASC', 'BEGIN', 'BETWEEN', 'BY',
    'CASCADE', 'CASE', 'CAST', 'CHECK', 'COLLATE', 'COLUMN', 'COMMENT', 'COMMIT',
    'CONCURRENTLY', 'CONFLICT', 'CONSTRAINT', 'CREATE', 'CROSS', 'CURRENT_DATE',
    'CURRENT_TIME', 'CURRENT_TIMESTAMP', 'DECLARE', 'DEFAULT', 'DEFERRABLE', 'DEFERRED',
    'DEFINER', 'DELETE', 'DESC', 'DESCRIBE', 'DISTINCT', 'DO', 'DROP', 'ELSE', 'ELSIF', 'END',
    'ENUM', 'ESCAPE', 'EXCEPT', 'EXCLUDED', 'EXEC', 'EXECUTE', 'EXISTS', 'EXPLAIN',
    'EXTENSION', 'FALSE', 'FOR', 'FOREIGN', 'FROM', 'FULL', 'FUNCTION', 'GENERATED', 'GRANT',
    'GROUP', 'HAVING', 'IDENTITY', 'IF', 'ILIKE', 'IMMEDIATE', 'IMMUTABLE', 'IN', 'INDEX',
    'INITIALLY', 'INNER', 'INSERT', 'INTERSECT', 'INTO', 'INVOKER', 'IS', 'ISNULL', 'JOIN',
    'KEY', 'LANGUAGE', 'LEFT', 'LIKE', 'LIMIT', 'LOCAL', 'LOCK', 'LOCKED', 'LOOP', 'NATURAL',
    'NOT', 'NOTHING', 'NOTNULL', 'NOWAIT', 'NULL', 'OFFSET', 'ON', 'ONLY', 'OR', 'ORDER',
    'OUTER', 'OVER', 'PARTITION', 'PREPARE', 'PRIMARY', 'PROCEDURE', 'RECURSIVE',
    'REFERENCES', 'RELEASE', 'RENAME', 'REPLACE', 'RESTRICT', 'RETURN', 'RETURNING', 'REVOKE',
    'RIGHT', 'ROLLBACK', 'SAVEPOINT', 'SCHEMA', 'SECURITY', 'SELECT', 'SEQUENCE', 'SET',
    'SHARE', 'SHOW', 'SIMILAR', 'SKIP', 'STABLE', 'START', 'STORED', 'STRICT', 'TABLE',
    'TEMP', 'TEMPORARY', 'THEN', 'TO', 'TRANSACTION', 'TRIGGER', 'TRUE', 'TRUNCATE', 'TYPE',
    'UNION', 'UNIQUE', 'UNLOGGED', 'UNLOCK', 'UPDATE', 'USE', 'USING', 'VACUUM', 'VALUES',
    'VIEW', 'VIRTUAL', 'VOLATILE', 'WHEN', 'WHERE', 'WINDOW', 'WITH', 'WITHOUT', 'ZONE',
  ]),
}

const TYPES_COMMON = new Set([
  'any', 'bigint', 'bool', 'boolean', 'byte', 'char', 'dict', 'error', 'float', 'float32',
  'float64', 'int', 'int16', 'int32', 'int64', 'int8', 'list', 'never', 'number', 'object',
  'rune', 'set', 'str', 'string', 'tuple', 'uint', 'uint16', 'uint32', 'uint64', 'uint8',
  'unknown', 'void', 'Promise', 'Record', 'Array', 'Map', 'Set', 'Error', 'Context',
  'VARCHAR', 'TEXT', 'INTEGER', 'BOOLEAN', 'DATETIME', 'JSON', 'BIGINT', 'TIMESTAMP',
])

const SQL_TYPES = new Set([
  'BIGINT', 'BIGSERIAL', 'BIT', 'BOOL', 'BOOLEAN', 'BYTEA', 'CHAR', 'CHARACTER', 'CIDR',
  'DATE', 'DATETIME', 'DECIMAL', 'DOUBLE', 'FLOAT', 'FLOAT4', 'FLOAT8', 'INET', 'INT',
  'INT2', 'INT4', 'INT8', 'INTEGER', 'INTERVAL', 'JSON', 'JSONB', 'MACADDR', 'MONEY',
  'NUMERIC', 'OID', 'PRECISION', 'REAL', 'REGCLASS', 'SERIAL', 'SMALLINT', 'SMALLSERIAL',
  'TEXT', 'TIME', 'TIMESTAMP', 'TIMESTAMPTZ', 'TIMETZ', 'TSQUERY', 'TSVECTOR', 'UUID',
  'VARBIT', 'VARCHAR', 'XML',
])

function resolveFamily(langRaw: string): string {
  const lang = langRaw.trim().toLowerCase()
  if (['diff', 'patch'].includes(lang)) return 'diff'
  if (['json', 'jsonc'].includes(lang)) return 'json'
  if (['yaml', 'yml', 'toml', 'ini', 'env', 'dotenv', 'conf'].includes(lang)) return 'yaml'
  if (['py', 'python'].includes(lang)) return 'python'
  if (['sh', 'bash', 'zsh', 'shell', 'powershell', 'ps1', 'cmd', 'bat'].includes(lang)) return 'shell'
  if (['sql', 'postgres', 'postgresql', 'sqlite', 'mysql'].includes(lang)) return 'sql'
  if (['css', 'scss', 'less'].includes(lang)) return 'css'
  if (['html', 'xml', 'svg'].includes(lang)) return 'markup'
  if (['go', 'golang', 'ts', 'tsx', 'typescript', 'js', 'jsx', 'javascript', 'rs', 'rust', 'java', 'c', 'cpp', 'csharp', 'cs', 'swift', 'kotlin', 'proto', 'protobuf'].includes(lang)) {
    return 'c_like'
  }
  return ''
}

function buildRules(family: string): TokenRule[] {
  switch (family) {
    case 'json':
      return [
        { kind: 'comment', regex: /\/\/[^\n]*|\/\*[\s\S]*?\*\//y },
        { kind: 'prop', regex: /"(?:\\.|[^"\\\n])*"(?=\s*:)/y },
        { kind: 'string', regex: /"(?:\\.|[^"\\\n])*"/y },
        { kind: 'number', regex: /-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y },
        { kind: 'keyword', regex: /\b(?:true|false|null)\b/y },
      ]
    case 'yaml':
      return [
        { kind: 'comment', regex: /#[^\n]*/y },
        { kind: 'string', regex: /"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y },
        { kind: 'prop', regex: /(?:^|(?<=\n))[ \t-]*[A-Za-z0-9_.-]+(?=\s*[:=])/y },
        { kind: 'number', regex: /\b\d+(?:\.\d+)?\b/y },
        { kind: 'keyword', regex: /\b(?:true|false|null|yes|no|on|off)\b/iy },
      ]
    case 'python':
      return [
        { kind: 'comment', regex: /#[^\n]*/y },
        { kind: 'string', regex: /(?:[rRbBfFuU]{0,2})(?:"""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*')/y },
        { kind: 'meta', regex: /@[A-Za-z_][A-Za-z0-9_.]*/y },
        { kind: 'number', regex: /\b(?:0[xXbBoO][\da-fA-F_]+|\d[\d_]*(?:\.[\d_]+)?(?:[eE][+-]?\d+)?)\b/y },
        { kind: 'func', regex: /\b[A-Za-z_][A-Za-z0-9_]*(?=\s*\()/y },
      ]
    case 'shell':
      return [
        { kind: 'comment', regex: /#[^\n]*/y },
        { kind: 'string', regex: /"(?:\\.|[^"\\])*"|'[^']*'/y },
        { kind: 'meta', regex: /\$\{[^}\n]+\}|\$[A-Za-z_][A-Za-z0-9_]*/y },
        { kind: 'prop', regex: /(?:^|(?<=\s))--?[A-Za-z0-9][A-Za-z0-9_-]*/y },
        { kind: 'number', regex: /\b\d+(?:\.\d+)?\b/y },
      ]
    case 'sql':
      return [
        { kind: 'comment', regex: /--[^\n]*|\/\*[\s\S]*?\*\//y },
        { kind: 'string', regex: /\$([A-Za-z_][A-Za-z0-9_]*)?\$[\s\S]*?\$\1\$|'(?:''|[^'])*'/y },
        { kind: 'prop', regex: /"(?:""|[^"\n])*"|`(?:``|[^`\n])*`/y },
        { kind: 'meta', regex: /\$\d+\b|(?<![:\w]):[A-Za-z_][A-Za-z0-9_]*/y },
        { kind: 'number', regex: /\b\d+(?:\.\d+)?\b/y },
        { kind: 'func', regex: /\b[A-Za-z_][A-Za-z0-9_]*(?=\s*\()/y },
      ]
    case 'css':
      return [
        { kind: 'comment', regex: /\/\*[\s\S]*?\*\//y },
        { kind: 'string', regex: /"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y },
        { kind: 'meta', regex: /@[A-Za-z-]+|--[A-Za-z0-9-]+/y },
        { kind: 'prop', regex: /[A-Za-z-]+(?=\s*:)/y },
        { kind: 'number', regex: /#[0-9a-fA-F]{3,8}\b|-?\b\d+(?:\.\d+)?(?:px|rem|em|vh|vw|%|ms|s|deg|fr)?\b/y },
      ]
    case 'markup':
      return [
        { kind: 'comment', regex: /<!--[\s\S]*?-->/y },
        { kind: 'string', regex: /"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y },
        { kind: 'keyword', regex: /<\/?[A-Za-z][A-Za-z0-9:-]*|\/?>/y },
        { kind: 'prop', regex: /\b[A-Za-z_:][A-Za-z0-9_.:-]*(?=\s*=)/y },
      ]
    case 'c_like':
      return [
        { kind: 'comment', regex: /\/\/[^\n]*|\/\*[\s\S]*?\*\//y },
        { kind: 'string', regex: /`(?:\\.|[^`\\])*`|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y },
        { kind: 'meta', regex: /@[A-Za-z_][A-Za-z0-9_]*/y },
        { kind: 'number', regex: /\b(?:0[xXbBoO][\da-fA-F_]+|\d[\d_]*(?:\.[\d_]+)?(?:[eE][+-]?\d+)?)\b/y },
        { kind: 'func', regex: /\b[A-Za-z_][A-Za-z0-9_]*(?=\s*\()/y },
      ]
    default:
      return []
  }
}

const IDENT_RE = /[A-Za-z_][A-Za-z0-9_]*/y

const MAX_HIGHLIGHT_CHARS = 8000
const MAX_HIGHLIGHT_LINES = 160

function exceedsLineCap(code: string, maxLines: number): boolean {
  let lines = 1
  for (let i = 0; i < code.length; i += 1) {
    if (code.charCodeAt(i) === 10) {
      lines += 1
      if (lines > maxLines) return true
    }
  }
  return false
}

interface HighlightSegment {
  kind: TokenKind | 'plain'
  text: string
}

function tokenizeCode(
  code: string,
  language: string,
  options?: { maxChars?: number; maxLines?: number },
): HighlightSegment[] | null {
  const maxChars = options?.maxChars ?? MAX_HIGHLIGHT_CHARS
  const maxLines = options?.maxLines ?? MAX_HIGHLIGHT_LINES
  if (!code || code.length > maxChars || exceedsLineCap(code, maxLines)) return null
  const family = resolveFamily(language)
  if (!family) return null

  if (family === 'diff') {
    const lines = code.split('\n')
    return lines.map((line, i) => {
      const text = i < lines.length - 1 ? `${line}\n` : line
      if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('@@')) {
        return { kind: 'meta', text }
      }
      if (line.startsWith('+')) return { kind: 'add', text }
      if (line.startsWith('-')) return { kind: 'del', text }
      return { kind: 'plain', text }
    })
  }

  const rules = buildRules(family)
  const keywords = KEYWORDS_BY_FAMILY[family]
  const out: HighlightSegment[] = []
  let plain = ''
  let pos = 0

  const flushPlain = () => {
    if (plain) {
      out.push({ kind: 'plain', text: plain })
      plain = ''
    }
  }

  while (pos < code.length) {
    let matched = false
    for (const rule of rules) {
      rule.regex.lastIndex = pos
      const m = rule.regex.exec(code)
      if (m && m[0].length > 0) {
        const text = m[0]
        let kind: TokenKind = rule.kind
        if (kind === 'func' && keywords && (keywords.has(text) || (family === 'sql' && keywords.has(text.toUpperCase())))) {
          kind = 'keyword'
        } else if (kind === 'func' && family === 'sql' && SQL_TYPES.has(text.toUpperCase())) {
          kind = 'type'
        }
        flushPlain()
        out.push({ kind, text })
        pos += text.length
        matched = true
        break
      }
    }
    if (matched) continue

    if (keywords) {
      IDENT_RE.lastIndex = pos
      const ident = IDENT_RE.exec(code)
      if (ident && ident[0].length > 0) {
        const word = ident[0]
        const upper = family === 'sql' ? word.toUpperCase() : word
        const isKw = family === 'sql' ? keywords.has(upper) : keywords.has(word)
        const isType = !isKw && (
          family === 'sql'
            ? SQL_TYPES.has(upper)
            : (family === 'c_like' || family === 'python') && TYPES_COMMON.has(word)
        )
        if (isKw || isType) {
          flushPlain()
          out.push({ kind: isKw ? 'keyword' : 'type', text: word })
        } else {
          plain += word
        }
        pos += word.length
        continue
      }
    }

    plain += code[pos]
    pos += 1
  }

  flushPlain()
  return out
}

export function highlightCode(
  code: string,
  language: string,
  options?: { maxChars?: number; maxLines?: number },
): ReactNode {
  const segments = tokenizeCode(code, language, options)
  if (!segments) return code
  return segments.map((seg, i) =>
    seg.kind === 'plain'
      ? seg.text
      : <span key={i} className={`tok-${seg.kind}`}>{seg.text}</span>,
  )
}

export function highlightCodeLines(
  code: string,
  language: string,
  options?: { maxChars?: number; maxLines?: number },
): ReactNode[][] {
  const normalized = code.replace(/\r\n/g, '\n')
  const segments = tokenizeCode(normalized, language, options)
  if (!segments) {
    return normalized.split('\n').map((line) => (line ? [line] : []))
  }
  const lines: ReactNode[][] = [[]]
  let key = 0
  for (const seg of segments) {
    const parts = seg.text.split('\n')
    for (let i = 0; i < parts.length; i += 1) {
      if (i > 0) lines.push([])
      const part = parts[i]
      if (!part) continue
      const current = lines[lines.length - 1]
      current.push(
        seg.kind === 'plain'
          ? part
          : <span key={key++} className={`tok-${seg.kind}`}>{part}</span>,
      )
    }
  }
  return lines
}
