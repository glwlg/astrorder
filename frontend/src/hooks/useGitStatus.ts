import { useQuery } from '@tanstack/react-query'
import { request } from '../api/client'
import type { Session } from '../domain/types'

export interface GitStatus {
  branch: string
  branches: string[]
  changed_files: number
  insertions: number
  deletions: number
  files: { path: string; status: string; staged: boolean }[]
}

export function useGitStatus(session: Session) {
  return useQuery({
    queryKey: ['astrorder', 'git-status', session.agent_id, session.id, session.workspace, session.connection_id],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({ workspace: session.workspace || '', session_id: session.id })
      if (session.connection_id) params.set('connection_id', session.connection_id)
      return request<GitStatus>(`/git/status?${params.toString()}`, { signal })
    },
    enabled: Boolean(session.workspace),
    retry: false,
    staleTime: 45_000,
    refetchIntervalInBackground: false,
    refetchInterval: () => (typeof document !== 'undefined' && document.visibilityState === 'hidden' ? false : 90_000),
  })
}
