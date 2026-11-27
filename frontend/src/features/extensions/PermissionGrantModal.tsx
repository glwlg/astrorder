import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Checkbox, Group, Modal, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconShieldCheck, IconShieldLock } from '@tabler/icons-react'
import type { PluginView } from '../../api/extensions'
import { useIsOfficeProfile } from '../office/appProfile'
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
  const isOfficeProfile = useIsOfficeProfile()
  const [unsignedEsmAcknowledged, setUnsignedEsmAcknowledged] = useState(false)

  useEffect(() => {
    if (!opened) {
      setUnsignedEsmAcknowledged(false)
    }
  }, [opened, plugin?.id])

  const isUnsignedExternalEsm = Boolean(
    plugin &&
      plugin.source === 'external' &&
      plugin.frontend?.mode === 'esm' &&
      !plugin.signature_verified,
  )
  const blockedInOfficeMode = isOfficeProfile && isUnsignedExternalEsm

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
              {plugin.frontend?.mode === 'esm' && (
                <Badge size="xs" variant="light" color={isUnsignedExternalEsm ? 'red' : 'teal'}>
                  {plugin.source === 'official'
                    ? '官方同页脚本'
                    : plugin.signature_verified
                      ? '已签名同页脚本'
                      : '未签名同页脚本'}
                </Badge>
              )}
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

        {isUnsignedExternalEsm && (
          <Alert
            color="red"
            variant="light"
            radius="md"
            icon={<IconAlertTriangle size={16} />}
            title="高风险提醒：未签名同页脚本插件拥有完整页面访问权限"
          >
            <Stack gap="xs">
              <Text size="xs" style={{ lineHeight: 1.6 }}>
                {blockedInOfficeMode
                  ? '办公模式下已严格禁止启用未签名的同页 ESM 脚本插件。如需调试第三方同页脚本，请切换至研发模式并确认来源可信。'
                  : '该外部插件声明以同页 ESM 脚本（frontend.mode = "esm"）运行且未通过 Ed25519 官方签名校验。它将在星序主页面同一 JS 上下文中执行，拥有完整页面访问权限（DOM、登录态与前端状态），仅在完全信任源码来源时启用。'}
              </Text>
              {mode === 'grant' && !blockedInOfficeMode && (
                <Checkbox
                  size="xs"
                  color="red"
                  checked={unsignedEsmAcknowledged}
                  onChange={(e) => setUnsignedEsmAcknowledged(e.currentTarget.checked)}
                  label="我已了解风险，允许在研发模式下加载未签名同页脚本插件（完全页面权限）"
                />
              )}
            </Stack>
          </Alert>
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
              color={isUnsignedExternalEsm ? 'red' : 'indigo'}
              loading={loading}
              disabled={blockedInOfficeMode || (isUnsignedExternalEsm && !unsignedEsmAcknowledged)}
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
