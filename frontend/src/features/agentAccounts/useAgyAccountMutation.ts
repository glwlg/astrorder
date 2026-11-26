import { notifications } from '@mantine/notifications'
import { useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query'
import { accountPlatformMeta, accountsApiFor, type AccountPlatform } from './agyAccountsApi'
import { platformLibraryOptions, platformTargetStateOptions } from './useAgyAccountOverview'

export type AccountAction =
  | { kind: 'save'; name: string }
  | { kind: 'switch'; id: string; force: boolean }
  | { kind: 'delete'; id: string }

export function accountActionMutationKey(environment: string, platform: AccountPlatform = 'antigravity') {
  return platform === 'antigravity'
    ? ['agy-account-action', environment]
    : ['agent-account-action', platform, environment]
}

export function useAgyAccountMutation(environment: string, platform: AccountPlatform = 'antigravity') {
  const client = useQueryClient()
  const meta = accountPlatformMeta(platform)
  const api = accountsApiFor(platform)
  const mutationKey = accountActionMutationKey(environment, platform)
  const isPending = useIsMutating({ mutationKey, exact: true }) > 0
  const mutation = useMutation({
    mutationKey,
    mutationFn: async ({ target, action }: { target: string; action: AccountAction }) => {
      if (action.kind === 'save') {
        await api.save(target, action.name)
        return { message: '当前账号已保存', warning: false }
      }
      if (action.kind === 'delete') {
        if (!api.deleteAccount) throw new Error(`${meta.label} 暂不支持删除已保存账号`)
        await api.deleteAccount(action.id)
        return { message: '已删除保存的账号', warning: false }
      }
      const result = await api.switch(target, action.id, action.force)
      return {
        message: result.restarted
          ? `账号已切换，${meta.shortLabel} 已重新接入`
          : `账号已切换，${meta.shortLabel} 重新接入未确认。可在连接管理中重新接入。`,
        warning: !result.restarted,
      }
    },
    onSuccess: ({ message, warning }) => notifications.show({ message, color: warning ? 'orange' : 'green' }),
    onError: error => notifications.show({ title: '账号操作未完成', message: error.message, color: 'red' }),
    onSettled: async (_, __, { target, action }) => {
      if (action.kind === 'save' || action.kind === 'delete') {
        await Promise.allSettled([client.fetchQuery({ ...platformLibraryOptions(platform), staleTime: 0, retry: false })])
      }
      if (target) {
        await Promise.allSettled([client.fetchQuery({ ...platformTargetStateOptions(target, platform), staleTime: 0, retry: false })])
      }
    },
  })
  function mutate(action: AccountAction) {
    const canRun = action.kind === 'delete' || Boolean(environment)
    if (canRun && !client.isMutating({ mutationKey, exact: true })) {
      mutation.mutate({ target: environment, action })
    }
  }
  return { ...mutation, variables: mutation.variables?.action, isPending, mutate }
}
