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
    'ADD', 'ALL', 'ALTER', 'AND', 'AS', 'ASC', 'BETWEEN', 'BY', 'CASE', 'CHECK', 'COLUMN',
    'COMMIT', 'CONFLICT', 'CONSTRAINT', 'CREATE', 'DEFAULT', 'DELETE', 'DESC', 'DISTINCT',
    'DO', 'DROP', 'ELSE', 'END', 'EXISTS', 'FALSE', 'FOREIGN', 'FROM', 'GROUP', 'HAVING',
    'IF', 'IN', 'INDEX', 'INNER', 'INSERT', 'INTO', 'IS', 'JOIN', 'KEY', 'LEFT', 'LIKE',
    'LIMIT', 'NOT', 'NOTHING', 'NULL', 'OFFSET', 'ON', 'OR', 'ORDER', 'OUTER', 'PRIMARY',
    'REFERENCES', 'RETURNING', 'RIGHT', 'ROLLBACK', 'SELECT', 'SET', 'TABLE', 'THEN',
    'TRUE', 'UNION', 'UNIQUE', 'UPDATE', 'VALUES', 'WHEN', 'WHERE', 'WITH',
  ]),
}

const TYPES_COMMON = new Set([
  'any', 'bigint', 'bool', 'boolean', 'byte', 'char', 'dict', 'error', 'float', 'float32',
  'float64', 'int', 'int16', 'int32', 'int64', 'int8', 'list', 'never', 'number', 'object',
  'rune', 'set', 'str', 'string', 'tuple', 'uint', 'uint16', 'uint32', 'uint64', 'uint8',
  'unknown', 'void', 'Promise', 'Record', 'Array', 'Map', 'Set', 'Error', 'Context',
  'VARCHAR', 'TEXT', 'INTEGER', 'BOOLEAN', 'DATETIME', 'JSON', 'BIGINT', 'TIMESTAMP',
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
        { kind: 'string', regex: /'(?:''|[^'])*'|"(?:\\.|[^"\\\n])*"/y },
        { kind: 'number', regex: /\b\d+(?:\.\d+)?\b/y },
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

export function highlightCode(code: string, language: string): ReactNode {
  if (!code || code.length > MAX_HIGHLIGHT_CHARS || exceedsLineCap(code, MAX_HIGHLIGHT_LINES)) return code
  const family = resolveFamily(language)
  if (!family) return code

  if (family === 'diff') {
    const lines = code.split('\n')
    return lines.map((line, i) => {
      const suffix = i < lines.length - 1 ? '\n' : ''
      if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('@@')) {
        return <span key={i} className="tok-meta">{line}{suffix}</span>
      }
      if (line.startsWith('+')) {
        return <span key={i} className="tok-add">{line}{suffix}</span>
      }
      if (line.startsWith('-')) {
        return <span key={i} className="tok-del">{line}{suffix}</span>
      }
      return `${line}${suffix}`
    })
  }

  const rules = buildRules(family)
  const keywords = KEYWORDS_BY_FAMILY[family]
  const out: ReactNode[] = []
  let plain = ''
  let pos = 0
  let key = 0

  const flushPlain = () => {
    if (plain) {
      out.push(plain)
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
        }
        flushPlain()
        out.push(<span key={key++} className={`tok-${kind}`}>{text}</span>)
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
        const isKw = family === 'sql' ? keywords.has(word.toUpperCase()) : keywords.has(word)
        const isType = !isKw && (family === 'c_like' || family === 'python' || family === 'sql') && TYPES_COMMON.has(word)
        if (isKw || isType) {
          flushPlain()
          out.push(<span key={key++} className={isKw ? 'tok-keyword' : 'tok-type'}>{word}</span>)
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
