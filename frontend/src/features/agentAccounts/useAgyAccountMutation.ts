import { notifications } from '@mantine/notifications'
import { useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query'
import { agyAccountsApi } from './agyAccountsApi'
import { libraryOptions, targetStateOptions } from './useAgyAccountOverview'

type AccountAction = { kind: 'save'; name: string } | { kind: 'switch'; id: string; force: boolean }

export function useAgyAccountMutation(environment: string) {
  const client = useQueryClient()
  const mutationKey = ['agy-account-action', environment]
  const isPending = useIsMutating({ mutationKey, exact: true }) > 0
  const mutation = useMutation({
    mutationKey,
    mutationFn: async ({ target, action }: { target: string; action: AccountAction }) => {
      if (action.kind === 'save') {
        await agyAccountsApi.save(target, action.name)
        return { message: '当前账号已保存', warning: false }
      }
      const result = await agyAccountsApi.switch(target, action.id, action.force)
      return { message: result.restarted ? '账号已切换，AGY 已重新接入' : '账号已切换，AGY 重新接入未确认。可在连接管理中重新接入。', warning: !result.restarted }
    },
    onSuccess: ({ message, warning }) => notifications.show({ message, color: warning ? 'orange' : 'green' }),
    onError: error => notifications.show({ title: '账号操作未完成', message: error.message, color: 'red' }),
    onSettled: async (_, __, { target, action }) => {
      if (action.kind === 'save') await Promise.allSettled([client.fetchQuery({ ...libraryOptions, staleTime: 0, retry: false })])
      await Promise.allSettled([client.fetchQuery({ ...targetStateOptions(target), staleTime: 0, retry: false })])
    },
  })
  function mutate(action: AccountAction) {
    if (environment && !client.isMutating({ mutationKey, exact: true })) mutation.mutate({ target: environment, action })
  }
  return { ...mutation, variables: mutation.variables?.action, isPending, mutate }
}
