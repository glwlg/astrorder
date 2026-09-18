import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { artifactViewerRegistry } from './registry'

/**
 * 分离文件路径与行号后缀，如 /path/to/file.py:123:4 -> { path: '/path/to/file.py', line: 123 }
 */
export function splitPathAndLine(raw: string): { path: string; line?: number } {
  let cleaned = decodeURIComponent(raw.trim().replace(/^[<"']|[>"']$/g, ''))
  const match = cleaned.match(/^(.*?):(\d+)(?::\d+)?$/)
  if (match) {
    return { path: match[1], line: parseInt(match[2], 10) }
  }
  return { path: cleaned }
}

export function resolveArtifactFromLocalPath(
  rawPath: string,
  session: Session,
  options?: { name?: string; mediaType?: string },
): ArtifactRef {
  const { path: baseCleaned, line } = splitPathAndLine(rawPath)
  let cleaned = baseCleaned

  const ws = session.workspace ? session.workspace.trim() : ''
  let fullPath = cleaned

  if (ws) {
    const isWindowsAbsolute = /^[a-zA-Z]:[\/\\]/.test(cleaned) || cleaned.startsWith('\\\\')
    const isLinuxAbsolute = /^\/(?:home|mnt|Users|tmp|var|private|opt|etc|usr|root)\b/i.test(cleaned)
    const isAlreadyFull = isWindowsAbsolute || (isLinuxAbsolute && (ws.startsWith('/') || cleaned.startsWith(ws)))

    if (!isAlreadyFull) {
      // 剥离可能存在的冗余前导斜杠 (如 AI 输出的 /backend/src/... 或 /frontend/...)
      const relativePart = cleaned.replace(/^[\/\\]+/, '')
      const sep = ws.includes('\\') ? '\\' : '/'
      fullPath = `${ws}${sep}${relativePart}`
    }
  }

  const fileName = options?.name
    ? decodeURIComponent(options.name)
    : fullPath.split(/[\/\\]/).pop() || 'file'

  // 创建临时工件用于探测最佳 Viewer
  const dummyArtifact: ArtifactRef = {
    id: `local:${session.id}:${fullPath}`,
    sessionId: session.id,
    agentId: session.agent_id,
    connectionId: session.connection_id || undefined,
    name: fileName,
    kind: 'workspace_file',
    path: fullPath,
    readUrl: '',
    mediaType: options?.mediaType || 'text/plain',
    writable: false,
  }

  const matchedViewer = artifactViewerRegistry.findViewer(dummyArtifact)

  const queryParams = new URLSearchParams()
  queryParams.set('path', fullPath)
  queryParams.set('session_id', session.id)
  if (session.connection_id) {
    queryParams.set('connection_id', session.connection_id)
  }
  try {
    const token = localStorage.getItem('astrorder:token')
    if (token) {
      queryParams.set('token', token)
    }
  } catch {}

  let readUrl = `/api/v1/files/raw?${queryParams.toString()}`
  if (cleaned.startsWith('/api/v1/files/raw') || cleaned.startsWith('http://') || cleaned.startsWith('https://')) {
    readUrl = cleaned
  }

  return {
    id: `local:${session.id}:${fullPath}`,
    sessionId: session.id,
    agentId: session.agent_id,
    connectionId: session.connection_id || undefined,
    name: fileName,
    kind: 'workspace_file',
    path: fullPath,
    readUrl,
    mediaType: matchedViewer?.mimeTypes?.[0] || options?.mediaType || 'text/plain',
    writable: matchedViewer?.capabilities.canEdit ?? true,
    metadata: line ? { line } : undefined,
  }
}

// 别名导出
export const resolveArtifactFromPath = resolveArtifactFromLocalPath
