const LOCAL_FILE_RE = /^(?:file:\/\/\/?|[a-zA-Z]:[/\\]|\/(?!\/|api\/)|\.{1,2}[/\\]|[^:#?\s]+\.[a-zA-Z0-9]{1,8}(?:[?#]|$))/

export function isMobileLocalFilePath(url: string): boolean {
  return LOCAL_FILE_RE.test(url) && !/^https?:\/\//i.test(url)
}

export function resolveMobileFilePath(raw: string, workspace?: string | null): string {
  const decoded = decodeURIComponent(raw.trim().replace(/^<|>$/g, '').replace(/^file:\/\/\/?/, ''))
  if (/^[a-zA-Z]:[/\\]/.test(decoded) || decoded.startsWith('/')) return decoded
  if (!workspace) return decoded
  const sep = workspace.includes('\\') ? '\\' : '/'
  return `${workspace.replace(/[/\\]+$/, '')}${sep}${decoded}`
}

export function mobileFileUrl(path: string, workspace?: string | null, sessionId?: string, connectionId?: string | null): string {
  const query = new URLSearchParams({ path: resolveMobileFilePath(path, workspace) })
  if (sessionId) query.set('session_id', sessionId)
  if (connectionId) query.set('connection_id', connectionId)
  return `/api/v1/files/raw?${query.toString()}`
}
