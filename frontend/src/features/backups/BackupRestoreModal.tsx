import { Alert, Button, Code, Group, Modal, Stack, Text } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api, ApiError, type BackupTarget, type BackupVersion } from '../../api/client'
import { agentKindLabel } from '../../components/AgentBrandIcon'
import { ForceAgentRestartDialog } from '../agents/ForceAgentRestartDialog'

export function BackupRestoreModal({ version, targets, onClose, onSettled }: {
  version: BackupVersion
  targets: BackupTarget[]
  onClose: () => void
  onSettled: () => void
}) {
  const queryClient = useQueryClient()
  const [confirmForce, setConfirmForce] = useState(false)
  const preview = useQuery({
    queryKey: ['astrorder', 'backup-restore-plan', version.id],
    queryFn: () => api.getBackupRestorePlan(version.id),
    retry: false,
  })
  const restore = useMutation({
    mutationFn: (confirmation: string) => api.restoreBackupVersion(version.id, confirmation),
    onSettled: () => {
      onSettled()
      if (preview.data?.post_restore_restart_agent) {
        void queryClient.invalidateQueries({ queryKey: ['astrorder', 'environments'] })
        void queryClient.invalidateQueries({ queryKey: ['astrorder', 'connections'] })
      }
    },
  })
  const plan = preview.data
  const target = targets.find((item) => item.id === plan?.connection_id)
  const result = restore.data
  const restartAgent = plan?.post_restore_restart_agent
  const forceRestart = useMutation({
    mutationFn: (environment: string) => api.restartEnvironmentAgent(environment, 'antigravity', true),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['astrorder', 'environments'] })
      void queryClient.invalidateQueries({ queryKey: ['astrorder', 'connections'] })
    },
  })
  const agentEnvironment = plan?.restore_agent_environment_name || restartAgent?.environment_id
  const canForceRestart = result?.hook_status === 'failed' && restartAgent?.kind === 'antigravity' && !forceRestart.isSuccess
  const pending = restore.isPending || forceRestart.isPending
  const error = restore.error ?? preview.error

  return (
    <Modal
      opened
      onClose={onClose}
      title={`还原备份 · ${version.version_label}`}
      centered
      closeOnEscape={!pending && !confirmForce}
      closeOnClickOutside={!pending && !confirmForce}
      withCloseButton={!pending && !confirmForce}
    >
      <Stack gap="sm">
        {preview.isFetching && !plan && <Text role="status">正在读取还原目标…</Text>}
        {plan && (
          <>
            <Text size="sm">{target?.name || plan.connection_id}</Text>
            <Code block style={{ overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' }}>{plan.target_path}</Code>
            {!result && <Text size="sm">同名文件将被覆盖，目标中的其他文件会保留。</Text>}
            {plan.post_restore_command && (
              <Stack gap={4}>
                <Text size="sm" fw={500}>还原后命令（{plan.hook_shell}）</Text>
                <Code block style={{ overflowWrap: 'anywhere', whiteSpace: 'pre-wrap', maxHeight: 180 }}>
                  {plan.post_restore_command}
                </Code>
              </Stack>
            )}
            {restartAgent && (
              <Text size="sm">
                还原成功后重启 {agentEnvironment} · {agentKindLabel(restartAgent.kind)}
              </Text>
            )}
          </>
        )}
        {restore.isPending && <Text role="status">正在校验并还原备份…</Text>}
        {error && (
          <Alert color="red" role="alert">
            {error instanceof ApiError ? error.detail : '无法确认还原状态，请检查连接和目标文件。'}
          </Alert>
        )}
        {result && (
          <Alert color={result.hook_status === 'failed' && !forceRestart.isSuccess ? 'orange' : 'teal'} role="status">
            {result.file_count > 0 ? `已还原 ${result.file_count} 个文件。` : '还原完成。'}
            {result.hook_status === 'success' && (restartAgent ? 'Agent 已重启。' : '还原后命令已执行。')}
            {forceRestart.isSuccess && 'Antigravity 已重启。'}
            {result.hook_status === 'failed' && !forceRestart.isSuccess && (
              forceRestart.error instanceof ApiError ? forceRestart.error.detail : result.hook_error || '还原后操作执行失败。'
            )}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button data-autofocus variant="default" disabled={pending} onClick={onClose}>
            {result || error ? '关闭' : '取消'}
          </Button>
          {canForceRestart && (
            <Button color="red" loading={forceRestart.isPending} onClick={() => setConfirmForce(true)}>强制重启 Antigravity</Button>
          )}
          {!result && !error && (
            <Button
              color="red"
              disabled={!plan || preview.isFetching}
              loading={restore.isPending}
              onClick={() => { if (plan) restore.mutate(plan.confirmation) }}
            >
              确认还原
            </Button>
          )}
        </Group>
        {confirmForce && restartAgent && agentEnvironment && (
          <ForceAgentRestartDialog
            environment={agentEnvironment}
            onClose={() => setConfirmForce(false)}
            onConfirm={() => forceRestart.mutate(restartAgent.environment_id)}
          />
        )}
      </Stack>
    </Modal>
  )
}
