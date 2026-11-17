import { useQuery } from '@tanstack/react-query'
import { authenticatedApiFetch } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSidecarStore } from '../sidecar/sidecarStore'

export function WorkspaceDiffPreview({ session, path, revision, onOpen }: {
  session: Session; path: string; revision: number; onOpen: () => void
}) {
  const params = new URLSearchParams({ path, workspace: session.workspace || '', session_id: session.id })
  if (session.connection_id) params.set('connection_id', session.connection_id)
  const readUrl = `/api/v1/git/diff-raw?${params.toString()}`
  const diff = useQuery({
    queryKey: ['astrorder', 'workbench-diff', session.agent_id, readUrl, revision],
    queryFn: async ({ signal }) => {
      const response = await authenticatedApiFetch(readUrl, { signal })
      if (!response.ok) throw new Error(`无法读取 Diff（${response.status}）`)
      return response.text()
    },
    retry: false,
    staleTime: 15_000,
    gcTime: 30_000,
  })
  const lines = diff.data?.trimEnd().split('\n') || []
  const start = lines.findIndex(line => line.startsWith('@@'))
  const preview = lines.slice(Math.max(0, start), Math.max(0, start) + 10)
  const openFullDiff = () => {
    useSidecarStore.getState().openArtifact({
    id: `gitdiff:${session.id}:${path}`, name: `Diff: ${path.split('/').pop()}`,
    kind: 'workspace_file', mediaType: 'text/x-diff', readUrl, writable: false, path,
    sessionId: session.id, agentId: session.agent_id, connectionId: session.connection_id || undefined,
    }, 'diff-viewer')
    onOpen()
  }
  return (
    <section className="workbench-diff-preview" aria-label={`${path} 差异预览`}>
      <header><span title={path}>{path.split('/').pop()}</span><button type="button" onClick={openFullDiff}>完整 Diff ↗</button></header>
      {diff.isError ? <p className="workbench-empty" role="alert">{diff.error.message}</p>
        : diff.isPending ? <p className="workbench-empty" role="status">正在读取差异…</p>
        : !diff.data ? <p className="workbench-empty">暂无文本差异</p>
        : <pre>{preview.map((line, i) => <span key={i}
          className={line.startsWith('+') ? 'diff-line-add' : line.startsWith('-') ? 'diff-line-delete' : line.startsWith('@@') ? 'diff-line-header' : undefined}>{line || ' '}</span>)}</pre>}
    </section>
  )
}
