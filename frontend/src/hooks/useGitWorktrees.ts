import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { GitWorktreesResponse } from '../domain/types'

export function gitWorktreesQueryKey(workspace?: string | null, connectionId?: string | null, sessionId?: string | null, includeStatuses = true) {
  return ['astrorder', 'git-worktrees', workspace || '', connectionId || '', sessionId || '', includeStatuses] as const
}

export function useGitWorktrees(
  params: {
    workspace?: string | null
    connectionId?: string | null
    sessionId?: string | null
    includeStatuses?: boolean
    enabled?: boolean
  },
) {
  const includeStatuses = params.includeStatuses ?? true
  const enabled = (params.enabled ?? true) && Boolean(params.workspace?.trim())
  return useQuery<GitWorktreesResponse>({
    queryKey: gitWorktreesQueryKey(params.workspace, params.connectionId, params.sessionId, includeStatuses),
    queryFn: ({ signal }) => api.listGitWorktrees({
      workspace: params.workspace?.trim() || undefined,
      connection_id: params.connectionId || undefined,
      session_id: params.sessionId || undefined,
      include_statuses: includeStatuses,
    }, signal),
    enabled,
    retry: false,
    staleTime: 20_000,
    refetchIntervalInBackground: false,
  })
}
