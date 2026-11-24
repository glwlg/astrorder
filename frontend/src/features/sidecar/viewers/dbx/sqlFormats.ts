const SQL_FILE_PATTERN = /\.(?:sql|ddl|psql|plpgsql|plsql|tsql)$/i

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
