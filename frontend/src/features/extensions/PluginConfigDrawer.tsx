import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Drawer,
  Group,
  Loader,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCheck, IconPlugConnected, IconPuzzle } from '@tabler/icons-react'
import {
  extensionsApi,
  type ExtensionConfigResponse,
  type PluginView,
  type SettingField,
} from '../../api/extensions'
import { extensionRegistry, useExtensionRegistryVersion } from './extensionRegistry'
import { useExtensionStore } from './extensionStore'
import { SandboxedPluginFrame } from './SandboxedPluginFrame'
import './extensions.css'

export interface PluginConfigDrawerProps {
  opened: boolean
  pluginId: string | null
  title?: string
  onClose: () => void
}

interface ParsedSecretInfo {
  set: boolean
  masked?: string
}

function parseSecretMap(res: ExtensionConfigResponse | null | undefined): Record<string, ParsedSecretInfo> {
  const map: Record<string, ParsedSecretInfo> = {}
  if (!res) return map

  if (res.secrets && typeof res.secrets === 'object') {
    for (const [key, val] of Object.entries(res.secrets)) {
      if (typeof val === 'string') {
        map[key] = { set: val.length > 0, masked: val || undefined }
      } else if (val && typeof val === 'object') {
        map[key] = {
          set: Boolean(val.set ?? (val.masked && val.masked.length > 0)),
          masked: val.masked,
        }
      }
    }
  }
  if (res.masked_secrets && typeof res.masked_secrets === 'object') {
    for (const [key, masked] of Object.entries(res.masked_secrets)) {
      map[key] = {
        set: Boolean(masked && masked.length > 0),
        masked: masked || map[key]?.masked,
      }
    }
  }
  if (res.secrets_set && typeof res.secrets_set === 'object') {
    for (const [key, isSet] of Object.entries(res.secrets_set)) {
      map[key] = {
        set: Boolean(isSet),
        masked: map[key]?.masked,
      }
    }
  }
  return map
}

function buildInitialValues(schema: SettingField[], serverValues?: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  for (const field of schema) {
    if (field.type === 'secret') continue
    if (serverValues && serverValues[field.key] !== undefined) {
      result[field.key] = serverValues[field.key]
    } else if (field.default_value !== undefined) {
      result[field.key] = field.default_value
    } else if (field.type === 'boolean') {
      result[field.key] = false
    } else if (field.type === 'number') {
      result[field.key] = field.min ?? 0
    } else {
      result[field.key] = ''
    }
  }
  return result
}

function joinUiEntryUrl(uiBaseUrl: string, uiEntry: string): string {
  const base = uiBaseUrl.replace(/\/+$/, '')
  const entry = uiEntry.replace(/^\/+/, '')
  return `${base}/${entry}`
}

function SchemaSettingsForm({
  plugin,
  onClose,
}: {
  plugin: PluginView
  onClose: () => void
}) {
  const schema = useMemo(() => plugin.settings?.schema || [], [plugin.settings?.schema])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [values, setValues] = useState<Record<string, unknown>>(() => buildInitialValues(schema))
  const [secretDrafts, setSecretDrafts] = useState<Record<string, string>>({})
  const [secretStatus, setSecretStatus] = useState<Record<string, ParsedSecretInfo>>({})

  useEffect(() => {
    let active = true
    void extensionsApi
      .getExtensionConfig(plugin.id)
      .then((res) => {
        if (!active) return
        setValues(buildInitialValues(schema, res?.values))
        setSecretStatus(parseSecretMap(res))
        setLoading(false)
      })
      .catch((err) => {
        if (!active) return
        setValues(buildInitialValues(schema))
        setError(err instanceof Error ? err.message : '读取扩展配置失败')
        setLoading(false)
      })
    return () => {
      active = false
    }
  }, [plugin.id, schema])

  const collectSecretUpdates = (): Record<string, string> => {
    const secrets: Record<string, string> = {}
    for (const [key, val] of Object.entries(secretDrafts)) {
      if (val.trim() !== '') {
        secrets[key] = val
      }
    }
    return secrets
  }

  const validateRequired = (): string | null => {
    for (const field of schema) {
      if (!field.required) continue
      if (field.type === 'secret') {
        const hasExisting = Boolean(secretStatus[field.key]?.set)
        const hasDraft = Boolean(secretDrafts[field.key]?.trim())
        if (!hasExisting && !hasDraft) {
          return `请填写必填项：${field.label}`
        }
      } else if (field.type === 'boolean') {
        continue
      } else {
        const val = values[field.key]
        if (val === undefined || val === null || String(val).trim() === '') {
          return `请填写必填项：${field.label}`
        }
      }
    }
    return null
  }

  const handleSave = async () => {
    const validationErr = validateRequired()
    if (validationErr) {
      setError(validationErr)
      return
    }
    setSaving(true)
    setError(null)
    try {
      const secrets = collectSecretUpdates()
      const updated = await extensionsApi.updateExtensionConfig(plugin.id, {
        values,
        ...(Object.keys(secrets).length > 0 ? { secrets } : {}),
      })
      if (updated) {
        setValues(buildInitialValues(schema, updated.values ?? values))
        setSecretStatus((prev) => {
          const parsed = parseSecretMap(updated)
          const merged = { ...prev, ...parsed }
          for (const key of Object.keys(secrets)) {
            if (!merged[key]?.set) {
              merged[key] = { set: true, masked: '••••••' }
            }
          }
          return merged
        })
      }
      setSecretDrafts({})
      notifications.show({
        color: 'teal',
        message: `已保存「${plugin.name}」配置`,
      })
    } catch (err) {
      const msg = err instanceof Error ? err.message : '保存扩展配置失败'
      setError(msg)
      notifications.show({ color: 'red', message: msg })
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async () => {
    const endpoint = plugin.settings?.test_endpoint
    if (!endpoint) return
    setTesting(true)
    setTestResult(null)
    try {
      const secrets = collectSecretUpdates()
      const res = await extensionsApi.postExtensionEndpoint<{
        ok?: boolean
        message?: string
        detail?: string
      }>(plugin.id, endpoint, {
        values,
        ...(Object.keys(secrets).length > 0 ? { secrets } : {}),
      })
      const ok = res?.ok !== false
      const message = res?.message || res?.detail || (ok ? '连接测试通过' : '连接测试未通过')
      setTestResult({ ok, message })
      notifications.show({ color: ok ? 'teal' : 'red', message })
    } catch (err) {
      const message = err instanceof Error ? err.message : '连接测试失败'
      setTestResult({ ok: false, message })
      notifications.show({ color: 'red', message })
    } finally {
      setTesting(false)
    }
  }

  if (loading) {
    return (
      <Group justify="center" py="xl">
        <Loader size="sm" />
        <Text size="xs" c="dimmed">
          正在加载配置…
        </Text>
      </Group>
    )
  }

  return (
    <Stack gap="md">
      {error && (
        <Alert color="red" variant="light" radius="md">
          {error}
        </Alert>
      )}
      {testResult && (
        <Alert color={testResult.ok ? 'teal' : 'red'} variant="light" radius="md">
          {testResult.message}
        </Alert>
      )}

      {schema.length === 0 ? (
        <Text size="sm" c="dimmed">
          该扩展暂无可配置的表单字段。
        </Text>
      ) : (
        schema.map((field) => {
          if (field.type === 'boolean') {
            return (
              <Group key={field.key} justify="space-between" align="flex-start" wrap="nowrap">
                <div>
                  <Text size="sm" fw={500}>
                    {field.label}
                  </Text>
                  {field.description && (
                    <Text size="xs" c="dimmed" mt={2}>
                      {field.description}
                    </Text>
                  )}
                </div>
                <Switch
                  aria-label={field.label}
                  checked={Boolean(values[field.key])}
                  onChange={(e) => {
                    const nextChecked = e.currentTarget.checked
                    setValues((prev) => ({ ...prev, [field.key]: nextChecked }))
                  }}
                />
              </Group>
            )
          }

          if (field.type === 'secret') {
            const info = secretStatus[field.key]
            const isSet = Boolean(info?.set)
            return (
              <div key={field.key}>
                <Group justify="space-between" mb={4}>
                  <Text size="sm" fw={500}>
                    {field.label}
                    {field.required && !isSet && (
                      <Text span c="red" ml={3}>
                        *
                      </Text>
                    )}
                  </Text>
                  {isSet && (
                    <Badge size="xs" variant="light" color="teal" leftSection={<IconCheck size={10} />}>
                      已配置{info?.masked ? ` (${info.masked})` : ''}
                    </Badge>
                  )}
                </Group>
                {field.description && (
                  <Text size="xs" c="dimmed" mb={6}>
                    {field.description}
                  </Text>
                )}
                <PasswordInput
                  size="sm"
                  aria-label={field.label}
                  placeholder={
                    isSet
                      ? '已安全保存，留空将保持原密钥不变'
                      : field.placeholder || `请输入${field.label}`
                  }
                  value={secretDrafts[field.key] ?? ''}
                  onChange={(e) => {
                    const nextValue = e.currentTarget.value
                    setSecretDrafts((prev) => ({ ...prev, [field.key]: nextValue }))
                  }}
                />
              </div>
            )
          }

          if (field.type === 'number') {
            const rawVal = values[field.key]
            const numVal =
              typeof rawVal === 'number'
                ? rawVal
                : rawVal === '' || rawVal === undefined || rawVal === null
                  ? ''
                  : Number(rawVal)
            return (
              <NumberInput
                key={field.key}
                size="sm"
                label={field.label}
                description={field.description}
                required={field.required}
                placeholder={field.placeholder}
                min={field.min}
                max={field.max}
                step={field.step}
                value={Number.isNaN(numVal) ? '' : numVal}
                onChange={(next) => setValues((prev) => ({ ...prev, [field.key]: next }))}
              />
            )
          }

          if (field.type === 'select') {
            return (
              <Select
                key={field.key}
                size="sm"
                label={field.label}
                description={field.description}
                required={field.required}
                placeholder={field.placeholder}
                data={field.options || []}
                value={values[field.key] !== undefined && values[field.key] !== null ? String(values[field.key]) : null}
                onChange={(next) => setValues((prev) => ({ ...prev, [field.key]: next ?? '' }))}
              />
            )
          }

          return (
            <TextInput
              key={field.key}
              size="sm"
              label={field.label}
              description={field.description}
              required={field.required}
              placeholder={field.placeholder}
              value={String(values[field.key] ?? '')}
              onChange={(e) => {
                const nextValue = e.currentTarget.value
                setValues((prev) => ({ ...prev, [field.key]: nextValue }))
              }}
            />
          )
        })
      )}

      <Group justify="space-between" mt="sm" pt="sm" style={{ borderTop: '1px solid var(--st-hairline, var(--astr-border))' }}>
        <div>
          {plugin.settings?.test_endpoint && (
            <Button
              size="xs"
              variant="light"
              leftSection={<IconPlugConnected size={14} />}
              loading={testing}
              onClick={() => {
                void handleTest()
              }}
            >
              测试连接
            </Button>
          )}
        </div>
        <Group gap="xs">
          <Button size="xs" variant="default" onClick={onClose}>
            取消
          </Button>
          <Button
            size="xs"
            loading={saving}
            onClick={() => {
              void handleSave()
            }}
          >
            保存配置
          </Button>
        </Group>
      </Group>
    </Stack>
  )
}

export function PluginConfigDrawer({
  opened,
  pluginId,
  title,
  onClose,
}: PluginConfigDrawerProps) {
  const registryVersion = useExtensionRegistryVersion()
  const manifest = useExtensionStore((s) =>
    pluginId ? s.items.find((item) => item.id === pluginId) : undefined,
  )

  const builtin = useMemo(() => {
    void registryVersion
    return pluginId ? extensionRegistry.getBuiltin(pluginId) : undefined
  }, [registryVersion, pluginId])

  const BuiltinSettingsComponent = builtin?.settingsComponent
  const drawerTitle = builtin?.settingsTitle || manifest?.name || title || pluginId || '插件配置'
  const isExternal = manifest?.source === 'external'
  const customUiSrc =
    manifest?.settings?.mode === 'custom_ui' && manifest.settings.ui_entry && manifest.ui_base_url
      ? joinUiEntryUrl(manifest.ui_base_url, manifest.settings.ui_entry)
      : null

  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      size="lg"
      padding="lg"
      title={
        <Group gap="xs">
          <IconPuzzle size={18} color="var(--astr-indigo, #5B5BD6)" />
          <Text fw={650} size="md">
            {drawerTitle}
          </Text>
          {isExternal ? (
            <Badge size="xs" variant="light" color="indigo">
              第三方
            </Badge>
          ) : (
            <Badge size="xs" variant="light" color="gray">
              官方扩展
            </Badge>
          )}
          {manifest?.version && (
            <Badge size="xs" variant="outline" color="gray">
              v{manifest.version}
            </Badge>
          )}
        </Group>
      }
    >
      <div className="ext-config-drawer-body">
        {BuiltinSettingsComponent ? (
          <BuiltinSettingsComponent embedded onClose={onClose} />
        ) : customUiSrc && manifest ? (
          <SandboxedPluginFrame
            pluginId={manifest.id}
            src={customUiSrc}
            title={`${manifest.name} 设置`}
            autoResize
            minHeight={420}
          />
        ) : manifest?.settings?.mode === 'schema' ? (
          <SchemaSettingsForm key={manifest.id} plugin={manifest} onClose={onClose} />
        ) : (
          <Stack gap="sm" py="md">
            <Text size="sm" c="dimmed">
              当前插件没有提供独立配置项。
            </Text>
            <Group justify="flex-end">
              <Button size="xs" variant="default" onClick={onClose}>
                关闭
              </Button>
            </Group>
          </Stack>
        )}
      </div>
    </Drawer>
  )
}
