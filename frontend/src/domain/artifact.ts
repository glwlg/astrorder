export type ArtifactKind = 'workspace_file' | 'attachment' | 'remote_url'

export interface ArtifactRef {
  /** 唯一标识，如 `session:drawio:test.drawio` */
  id: string
  sessionId: string
  agentId: string
  connectionId?: string
  name: string
  kind: ArtifactKind
  /** 本地工作区中的文件路径 */
  path?: string
  /** 读取内容的 URL */
  readUrl: string
  /** 媒体类型，例如 application/vnd.jgraph.mxfile */
  mediaType: string
  /** 是否支持保存写回 */
  writable: boolean
}
