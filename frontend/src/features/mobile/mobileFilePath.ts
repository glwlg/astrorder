import { imageMediaType } from '../sidecar/viewers/image/formats'
import { videoMediaType } from '../sidecar/viewers/video/formats'

const LOCAL_FILE_RE = /^(?:file:\/\/\/?|[a-zA-Z]:[/\\]|\/(?!\/|api\/)|\.{1,2}[/\\]|[^:#?\s]+\.[a-zA-Z0-9]{1,8}(?:[?#]|$))/

function safeDecodeURIComponent(value: string): string {
  try {
    return decodeURIComponent(value.replace(/%(?![0-9a-fA-F]{2})/g, '%25'))
  } catch {
    return value
  }
}

export function isMobileLocalFilePath(url: string): boolean {
  return LOCAL_FILE_RE.test(url) && !/^https?:\/\//i.test(url)
}

export function isMobileImageFilePath(path: string): boolean {
  const clean = path.trim().split(/[?#]/)[0]
  if (!clean || /\.drawio\.svg$/i.test(clean)) return false
  return Boolean(imageMediaType(clean))
}

export function isMobileVideoFilePath(path: string): boolean {
  const clean = path.trim().split(/[?#]/)[0]
  if (!clean) return false
  return Boolean(videoMediaType(clean))
}

export function resolveMobileFilePath(raw: string, workspace?: string | null): string {
  const unquoted = raw.trim().replace(/^<|>$/g, '')
  let decoded = ''
  if (/^file:\/\//i.test(unquoted)) {
    try {
      const fileUrl = new URL(unquoted)
      let pathname = fileUrl.hostname && fileUrl.hostname.toLowerCase() !== 'localhost'
        ? `//${fileUrl.hostname}${fileUrl.pathname}`
        : fileUrl.pathname
      pathname = safeDecodeURIComponent(pathname)
      if (/^\/[a-zA-Z]:[/\\]/.test(pathname)) pathname = pathname.slice(1)
      decoded = pathname
    } catch {
      const stripped = safeDecodeURIComponent(unquoted.replace(/^file:\/\/(?:localhost)?/i, ''))
      decoded = /^\/[a-zA-Z]:[/\\]/.test(stripped) ? stripped.slice(1) : stripped
    }
  } else {
    decoded = safeDecodeURIComponent(unquoted).replace(/#L\d+(?:-L?\d+)?$/i, '')
  }

  if (/^[a-zA-Z]:[/\\]/.test(decoded) || decoded.startsWith('/') || decoded.startsWith('\\\\')) return decoded
  if (!workspace) return decoded
  const sep = workspace.includes('\\') ? '\\' : '/'
  return `${workspace.replace(/[/\\]+$/, '')}${sep}${decoded}`
}

export function mobileFileUrl(path: string, workspace?: string | null, sessionId?: string, connectionId?: string | null): string {
  const query = new URLSearchParams({ path: resolveMobileFilePath(path, workspace) })
  if (sessionId) query.set('session_id', sessionId)
  if (connectionId) query.set('connection_id', connectionId)
  try {
    const token = localStorage.getItem('astrorder:token')
    if (token) query.set('token', token)
  } catch {}
  return `/api/v1/files/raw?${query.toString()}`
}

const MARKDOWN_LINK_RE = /(!?)\[[^\]]*\]\((<[^>]+>|[^)\s]+)(?:\s+"[^"]*")?\)/g

export function extractMobileMarkdownImageUrls(
  markdown: string,
  workspace?: string | null,
  sessionId?: string,
  connectionId?: string | null,
): string[] {
  const urls: string[] = []
  const seen = new Set<string>()
  const push = (url: string) => {
    if (!url || seen.has(url)) return
    seen.add(url)
    urls.push(url)
  }
  for (const match of markdown.matchAll(MARKDOWN_LINK_RE)) {
    const isImg = match[1] === '!'
    const rawTarget = (match[2] || '').trim()
    if (!rawTarget) continue
    if (isMobileLocalFilePath(rawTarget)) {
      const resolved = resolveMobileFilePath(rawTarget, workspace)
      if (isImg || isMobileImageFilePath(resolved)) {
        push(mobileFileUrl(rawTarget, workspace, sessionId, connectionId))
      }
    } else if (isImg && /^(?:https?:\/\/|\/api\/v1\/)/i.test(rawTarget)) {
      push(rawTarget)
    }
  }
  return urls
}
