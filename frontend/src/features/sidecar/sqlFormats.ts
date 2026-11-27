const SQL_FILE_PATTERN = /\.(?:sql|ddl|psql|plpgsql|plsql|tsql)$/i
const SQL_LANG_SET = new Set([
  'sql',
  'postgresql',
  'postgres',
  'pgsql',
  'plpgsql',
  'mysql',
  'mariadb',
  'sqlite',
  'tsql',
  'mssql',
  'plsql',
  'oracle',
  'ddl',
  'dml',
  'clickhouse',
  'duckdb',
  'snowflake',
  'bigquery',
  'hql',
  'hive',
])

export function isSqlLanguage(lang: string | undefined | null): boolean {
  if (!lang) return false
  return SQL_LANG_SET.has(lang.trim().toLowerCase())
}

export function buildSqlSnippetFilename(sql: string, hash: string): string {
  const cleaned = sql
    .replace(/--[^\r\n]*/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .trim()
  const match = cleaned.match(
    /^(select|insert|update|delete|alter|create|drop|truncate|with|explain|show|grant|revoke)\b[\s\S]{0,120}?\b(?:from|into|table|index|view|update|join)\s+["`[]?([a-zA-Z0-9_.-]+)["`\]]?/i,
  )
  if (match) {
    const verb = match[1].toLowerCase()
    const target = match[2].replace(/[^a-zA-Z0-9_.-]/g, '_').slice(0, 40)
    return `${verb}_${target}.sql`
  }
  const verbOnly = cleaned.match(/^(select|insert|update|delete|alter|create|drop|with|explain|show|grant|revoke)\b/i)
  const prefix = verbOnly ? verbOnly[1].toLowerCase() : 'query'
  return `${prefix}_${hash.slice(0, 6)}.sql`
}

export function stripSqlLocationSuffix(rawPath: string): string {
  return rawPath
    .trim()
    .split(/[?#]/)[0]
    .replace(/:\d+(?:[:-]\d+)?$/, '')
}

export function extractSqlLine(rawPath: string): number | undefined {
  const trimmed = rawPath.trim()
  const hashMatch = /#L(\d+)/i.exec(trimmed)
  if (hashMatch) {
    const n = Number(hashMatch[1])
    if (Number.isInteger(n) && n > 0) return n
  }
  const withoutQuery = trimmed.split(/[?#]/)[0]
  const colonMatch = /:(\d+)(?:[:-]\d+)?$/.exec(withoutQuery)
  if (colonMatch) {
    const n = Number(colonMatch[1])
    if (Number.isInteger(n) && n > 0) return n
  }
  return undefined
}

export function isSqlFilePath(rawPath: string): boolean {
  const cleaned = stripSqlLocationSuffix(rawPath)
  return Boolean(cleaned) && SQL_FILE_PATTERN.test(cleaned)
}

export function extractSqlFileName(rawPath: string): string {
  const cleaned = stripSqlLocationSuffix(rawPath)
  const parts = cleaned.split(/[/\\]/).filter(Boolean)
  return parts[parts.length - 1] || 'query.sql'
}
