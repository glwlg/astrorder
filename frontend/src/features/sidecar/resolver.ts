import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { artifactViewerRegistry } from './registry'

export function resolveArtifactFromLocalPath(
  path: string,
  session: Session,
  options?: { name?: string; mediaType?: string },
): ArtifactRef {
  const cleaned = decodeURIComponent(path.trim().replace(/^<|>$/g, ''))
  const fileName = options?.name ? decodeURIComponent(options.name) : cleaned.split(/[\\/]/).pop() || 'file'

  // 创建临时工件原型，委托注册表根据插件自声明匹配最佳 Viewer
  const dummyArtifact: ArtifactRef = {
    id: `local:${session.id}:${cleaned}`,
    sessionId: session.id,
    agentId: session.agent_id,
    connectionId: session.connection_id || undefined,
    name: fileName,
    kind: 'workspace_file',
    path: cleaned,
    readUrl: '',
    mediaType: options?.mediaType || 'text/plain',
    writable: false,
  }

  const matchedViewer = artifactViewerRegistry.findViewer(dummyArtifact)

  let fullPath = cleaned
  const queryParams = new URLSearchParams()
  queryParams.set('path', fullPath)
  queryParams.set('session_id', session.id)
  if (session.connection_id) {
    queryParams.set('connection_id', session.connection_id)
  }
  let readUrl = `/api/v1/files/raw?${queryParams.toString()}`

  if (cleaned.startsWith('/api/v1/files/raw') || cleaned.startsWith('http://') || cleaned.startsWith('https://')) {
    readUrl = cleaned
  } else if (session.workspace && !/^[a-zA-Z]:[/\\\\]/.test(cleaned) && !cleaned.startsWith('/')) {
    const separator = session.workspace.includes('\\') ? '\\' : '/'
    fullPath = `${session.workspace}${separator}${cleaned}`
    queryParams.set('path', fullPath)
    readUrl = `/api/v1/files/raw?${queryParams.toString()}`
  }

  return {
    id: `local:${session.id}:${cleaned}`,
    sessionId: session.id,
    agentId: session.agent_id,
    connectionId: session.connection_id || undefined,
    name: fileName,
    kind: 'workspace_file',
    path: fullPath,
    readUrl,
    mediaType: matchedViewer?.mimeTypes?.[0] || options?.mediaType || 'text/plain',
    writable: matchedViewer?.capabilities.canEdit ?? false,
  }
}

// 别名导出
export const resolveArtifactFromPath = resolveArtifactFromLocalPath
