import { Badge, Button, Group, Modal, Stack, Text } from '@mantine/core'
import { IconShieldCheck, IconShieldLock } from '@tabler/icons-react'
import type { PluginView } from '../../api/extensions'
import { getPermissionMeta } from './types'
import './extensions.css'

export interface PermissionGrantModalProps {
  opened: boolean
  plugin: PluginView | null
  pendingPermissions?: string[]
  mode?: 'grant' | 'view'
  loading?: boolean
  onConfirm?: (permissions: string[]) => void
  onClose: () => void
}

const RISK_LABEL: Record<'low' | 'medium' | 'high', { label: string; color: string }> = {
  high: { label: '敏感权限', color: 'red' },
  medium: { label: '常规权限', color: 'orange' },
  low: { label: '基础权限', color: 'gray' },
}

export function PermissionGrantModal({
  opened,
  plugin,
  pendingPermissions,
  mode = 'grant',
  loading = false,
  onConfirm,
  onClose,
}: PermissionGrantModalProps) {
  const grantedSet = new Set(plugin?.granted_permissions || [])
  const declared = plugin?.permissions || []
  const effectiveList =
    mode === 'grant' && pendingPermissions && pendingPermissions.length > 0
      ? Array.from(new Set([...declared, ...pendingPermissions]))
      : declared

  const permissionsToGrant = Array.from(
    new Set([...(plugin?.permissions || []), ...(pendingPermissions || [])]),
  )

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="md"
      radius="lg"
      centered
      title={
        <Group gap="xs">
          {mode === 'grant' ? (
            <IconShieldLock size={18} color="var(--astr-indigo, #5B5BD6)" />
          ) : (
            <IconShieldCheck size={18} color="var(--astr-green, #12b886)" />
          )}
          <Text fw={650} size="md">
            {mode === 'grant' ? '扩展权限授权确认' : '扩展权限清单'}
          </Text>
        </Group>
      }
    >
      <Stack gap="md">
        {plugin && (
          <div>
            <Group gap="xs" mb={4}>
              <Text fw={600} size="sm">
                {plugin.name}
              </Text>
              <Badge size="xs" variant="light" color="indigo">
                v{plugin.version}
              </Badge>
              {plugin.author && (
                <Text size="xs" c="dimmed">
                  by {plugin.author}
                </Text>
              )}
            </Group>
            <Text size="xs" c="dimmed" style={{ lineHeight: 1.6 }}>
              {mode === 'grant'
                ? '启用第三方扩展前，请确认是否授予以下访问权限。扩展仅能在授权范围内通过主机 API 访问受控能力。'
                : '以下是该扩展在 plugin.json 中声明的权限范围及当前授权状态。'}
            </Text>
          </div>
        )}

        {effectiveList.length === 0 ? (
          <Text size="xs" c="dimmed" py="xs">
            该扩展未申请任何额外主机权限。
          </Text>
        ) : (
          <Stack gap="xs">
            {effectiveList.map((perm) => {
              const meta = getPermissionMeta(perm)
              const riskMeta = RISK_LABEL[meta.risk]
              const isGranted = grantedSet.has(perm)
              const isPending =
                (pendingPermissions && pendingPermissions.includes(perm)) ||
                (!isGranted && mode === 'grant')
              return (
                <div key={perm} className="ext-permission-item">
                  <div style={{ minWidth: 0 }}>
                    <Group gap={6} mb={2}>
                      <Text size="xs" fw={600}>
                        {meta.label}
                      </Text>
                      <code style={{ fontSize: 11, color: 'var(--astr-muted)' }}>{perm}</code>
                    </Group>
                    <Text size="11.5px" c="dimmed" style={{ lineHeight: 1.5 }}>
                      {meta.description}
                    </Text>
                  </div>
                  <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
                    <Badge size="xs" variant="light" color={riskMeta.color}>
                      {riskMeta.label}
                    </Badge>
                    {mode === 'view' ? (
                      <Badge size="xs" variant="light" color={isGranted ? 'teal' : 'yellow'}>
                        {isGranted ? '已授权' : '待授权'}
                      </Badge>
                    ) : isPending ? (
                      <Badge size="xs" variant="outline" color="indigo">
                        本次授权
                      </Badge>
                    ) : null}
                  </Group>
                </div>
              )
            })}
          </Stack>
        )}

        <Group justify="flex-end" gap="xs" mt="xs">
          <Button size="xs" variant="default" onClick={onClose}>
            {mode === 'grant' ? '取消' : '关闭'}
          </Button>
          {mode === 'grant' && onConfirm && (
            <Button
              size="xs"
              color="indigo"
              loading={loading}
              onClick={() => onConfirm(permissionsToGrant)}
            >
              授权并启用
            </Button>
          )}
        </Group>
      </Stack>
    </Modal>
  )
}
