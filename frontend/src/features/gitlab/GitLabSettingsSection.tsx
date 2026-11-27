import { useCallback, useContext, useEffect, useState } from 'react'
import { QueryClientContext } from '@tanstack/react-query'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Code,
  Group,
  Modal,
  Paper,
  PasswordInput,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBrandGitlab,
  IconCheck,
  IconPencil,
  IconPlugConnected,
  IconPlus,
  IconShieldOff,
  IconTrash,
} from '@tabler/icons-react'
import { gitlabApi, type GitLabInstance } from '../../api/gitlab'

interface InstanceFormState {
  name: string
  base_url: string
  token: string
  insecure_skip_verify: boolean
  enabled: boolean
  is_default: boolean
}

const EMPTY_FORM: InstanceFormState = {
  name: '',
  base_url: '',
  token: '',
  insecure_skip_verify: false,
  enabled: true,
  is_default: false,
}

export interface GitLabSettingsSectionProps {
  embedded?: boolean
  onClose?: () => void
}

export function GitLabSettingsSection({ embedded = true, onClose: _onClose }: GitLabSettingsSectionProps = {}) {
  const queryClient = useContext(QueryClientContext)
  const [instances, setInstances] = useState<GitLabInstance[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpened, setModalOpened] = useState(false)
  const [editing, setEditing] = useState<GitLabInstance | null>(null)
  const [form, setForm] = useState<InstanceFormState>(EMPTY_FORM)
  const [saving, setSaving] = useState(false)
  const [testingId, setTestingId] = useState<string | null>(null)
  const [testingModal, setTestingModal] = useState(false)
  const [modalTestNote, setModalTestNote] = useState<{ ok: boolean; text: string } | null>(null)

  const loadInstances = useCallback(async () => {
    setLoading(true)
    try {
      const resp = await gitlabApi.listInstances()
      setInstances(Array.isArray(resp.items) ? resp.items : [])
    } catch {
      // Quietly handle initial load errors when backend is unavailable or in unit tests
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadInstances()
  }, [loadInstances])

  const invalidateCIQueries = () => {
    void queryClient?.invalidateQueries({ queryKey: ['astrorder', 'gitlab-pipelines'] })
  }

  const openCreateModal = () => {
    setEditing(null)
    setForm({ ...EMPTY_FORM, is_default: instances.length === 0 })
    setModalTestNote(null)
    setModalOpened(true)
  }

  const openEditModal = (inst: GitLabInstance) => {
    setEditing(inst)
    setForm({
      name: inst.name,
      base_url: inst.base_url,
      token: '',
      insecure_skip_verify: Boolean(inst.insecure_skip_verify),
      enabled: inst.enabled,
      is_default: Boolean(inst.is_default),
    })
    setModalTestNote(null)
    setModalOpened(true)
  }

  const handleTestModal = async () => {
    if (!form.base_url.trim()) {
      notifications.show({ color: 'orange', message: '请先填写 GitLab 地址' })
      return
    }
    setTestingModal(true)
    setModalTestNote(null)
    try {
      const resp = await gitlabApi.testInstance({
        instance_id: editing?.id,
        base_url: form.base_url.trim(),
        token: form.token.trim() || undefined,
        insecure_skip_verify: form.insecure_skip_verify,
      })
      const detail = resp.ok
        ? `连接成功：${resp.name || resp.username || '已认证'}${resp.version ? ` (GitLab ${resp.version})` : ''}`
        : resp.message || '连接失败'
      setModalTestNote({ ok: resp.ok, text: detail })
      notifications.show({ color: resp.ok ? 'blue' : 'red', message: detail })
    } catch (error) {
      const msg = error instanceof Error ? error.message : '测试连接失败'
      setModalTestNote({ ok: false, text: msg })
      notifications.show({ color: 'red', message: msg })
    } finally {
      setTestingModal(false)
    }
  }

  const handleSave = async () => {
    const baseUrl = form.base_url.trim()
    if (!baseUrl) {
      notifications.show({ color: 'orange', message: '请填写 GitLab 实例地址（例如 https://gitlab.company.com）' })
      return
    }
    if (!editing && !form.token.trim()) {
      notifications.show({ color: 'orange', message: '请填写 Personal Access Token（需至少包含 read_api 权限）' })
      return
    }
    setSaving(true)
    try {
      if (editing) {
        const payload = {
          name: form.name.trim(),
          base_url: baseUrl,
          insecure_skip_verify: form.insecure_skip_verify,
          enabled: form.enabled,
          is_default: form.is_default,
          ...(form.token.trim() ? { token: form.token.trim() } : {}),
        }
        await gitlabApi.updateInstance(editing.id, payload)
        notifications.show({ color: 'blue', message: `已更新 GitLab 实例「${form.name.trim() || baseUrl}」` })
      } else {
        await gitlabApi.createInstance({
          name: form.name.trim(),
          base_url: baseUrl,
          token: form.token.trim(),
          insecure_skip_verify: form.insecure_skip_verify,
          enabled: form.enabled,
          is_default: form.is_default,
        })
        notifications.show({ color: 'blue', message: `已添加 GitLab 实例「${form.name.trim() || baseUrl}」` })
      }
      setModalOpened(false)
      await loadInstances()
      invalidateCIQueries()
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '保存 GitLab 实例失败' })
    } finally {
      setSaving(false)
    }
  }

  const handleToggleEnabled = async (inst: GitLabInstance, enabled: boolean) => {
    try {
      await gitlabApi.updateInstance(inst.id, { enabled })
      await loadInstances()
      invalidateCIQueries()
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '更新状态失败' })
    }
  }

  const handleTestExisting = async (inst: GitLabInstance) => {
    setTestingId(inst.id)
    try {
      const resp = await gitlabApi.testInstance({ instance_id: inst.id })
      notifications.show({
        color: resp.ok ? 'blue' : 'red',
        message: resp.ok
          ? `${inst.name} 连接正常（${resp.username || 'OK'}${resp.version ? ` · v${resp.version}` : ''}）`
          : `${inst.name} 连接失败：${resp.message || '未知错误'}`,
      })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '连接测试失败' })
    } finally {
      setTestingId(null)
    }
  }

  const handleDelete = async (inst: GitLabInstance) => {
    try {
      await gitlabApi.deleteInstance(inst.id)
      notifications.show({ color: 'gray', message: `已移除 GitLab 实例「${inst.name}」` })
      await loadInstances()
      invalidateCIQueries()
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '删除失败' })
    }
  }

  return (
    <div style={embedded ? undefined : { marginTop: 28 }}>
      <Paper p={embedded ? 'sm' : 'md'} withBorder radius={embedded ? 'md' : 'lg'} mb="md" style={{ background: 'var(--astr-surface)' }}>
        <Group justify="space-between" align="center" wrap="wrap" gap="sm">
          <Group gap="sm" wrap="nowrap" align="flex-start" style={{ minWidth: 0, flex: 1 }}>
            <div
              style={{
                width: 38,
                height: 38,
                flexShrink: 0,
                borderRadius: 10,
                background: 'color-mix(in srgb, #e24329 12%, transparent)',
                color: '#e24329',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <IconBrandGitlab size={22} />
            </div>
            <div style={{ minWidth: 0, flex: 1 }}>
              <Title order={3} size="h4">GitLab CI / CD 集成</Title>
              <Text size="xs" c="dimmed" mt={2} style={{ overflowWrap: 'anywhere' }}>
                支持自建内网 GitLab 与 gitlab.com。配置后，对话底栏自动展示当前仓库 CI 流水线状态，Agent 也可随时查询流水线与失败日志。
              </Text>
            </div>
          </Group>

          <Group gap="xs">
            <Badge size="md" variant="light" color="gray">
              已配置 {instances.length} 个实例
            </Badge>
            <Button
              size="xs"
              color="dark"
              leftSection={<IconPlus size={15} />}
              onClick={openCreateModal}
            >
              添加 GitLab 实例
            </Button>
          </Group>
        </Group>
      </Paper>

      {instances.length === 0 && !loading ? (
        <Card withBorder radius="md" p="lg" style={{ background: 'var(--astr-surface)' }}>
          <Stack align="center" gap="xs" ta="center">
            <IconBrandGitlab size={30} color="var(--astr-muted)" />
            <Text fw={600} size="sm">尚未配置 GitLab 实例</Text>
            <Text size="xs" c="dimmed" maw={520}>
              添加公司自建 GitLab（如 <Code>https://gitlab.company.com</Code>）和 Personal Access Token 后，星序会按工作区
              <Code>.git/config</Code> 自动匹配仓库并在对话栏实时显示最新 CI 流水线；Agent 可直接运行
              <Code>astrorder-agent gitlab status</Code> / <Code>pipelines</Code> / <Code>log</Code> 查询。
            </Text>
            <Button
              mt="xs"
              size="xs"
              color="dark"
              leftSection={<IconPlus size={15} />}
              onClick={openCreateModal}
            >
              添加第一个 GitLab 实例
            </Button>
          </Stack>
        </Card>
      ) : (
        <Stack gap="sm">
          {instances.map((inst) => (
            <Card
              key={inst.id}
              withBorder
              radius="md"
              p="sm"
              style={{
                background: 'var(--astr-surface)',
                opacity: inst.enabled ? 1 : 0.65,
              }}
            >
              <Group justify="space-between" wrap="nowrap" gap="md">
                <Group gap="sm" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      width: 34,
                      height: 34,
                      borderRadius: 8,
                      border: '1px solid var(--astr-border)',
                      background: 'var(--astr-surface-muted)',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      flexShrink: 0,
                      color: '#e24329',
                    }}
                  >
                    <IconBrandGitlab size={18} />
                  </div>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <Group gap="xs" wrap="nowrap">
                      <Text fw={600} size="sm" truncate>{inst.name}</Text>
                      {inst.is_default && (
                        <Badge size="xs" variant="light" color="indigo">默认</Badge>
                      )}
                      {inst.insecure_skip_verify && (
                        <Tooltip label="已跳过自签名 TLS 证书校验" withArrow>
                          <Badge size="xs" variant="light" color="gray" leftSection={<IconShieldOff size={11} />}>
                            跳过 TLS 校验
                          </Badge>
                        </Tooltip>
                      )}
                      {!inst.enabled && (
                        <Badge size="xs" variant="outline" color="gray">已停用</Badge>
                      )}
                    </Group>
                    <Group gap="xs" mt={2} wrap="wrap">
                      <Text size="xs" c="dimmed" truncate>{inst.base_url}</Text>
                      {inst.token_set && (
                        <Text size="xs" c="dimmed" ff="monospace">
                          Token: {inst.masked_token || '已配置'}
                        </Text>
                      )}
                    </Group>
                  </div>
                </Group>

                <Group gap="xs" wrap="nowrap" style={{ flexShrink: 0 }}>
                  <Switch
                    size="sm"
                    aria-label={`启用 GitLab 实例 ${inst.name}`}
                    checked={inst.enabled}
                    onChange={(e) => { void handleToggleEnabled(inst, e.currentTarget.checked) }}
                  />
                  <Tooltip label="测试连通性" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="blue"
                      size="sm"
                      aria-label={`测试连接 ${inst.name}`}
                      loading={testingId === inst.id}
                      onClick={() => { void handleTestExisting(inst) }}
                    >
                      <IconPlugConnected size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="编辑" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size="sm"
                      aria-label={`编辑 GitLab 实例 ${inst.name}`}
                      onClick={() => openEditModal(inst)}
                    >
                      <IconPencil size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="删除" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="red"
                      size="sm"
                      aria-label={`删除 GitLab 实例 ${inst.name}`}
                      onClick={() => { void handleDelete(inst) }}
                    >
                      <IconTrash size={15} />
                    </ActionIcon>
                  </Tooltip>
                </Group>
              </Group>
            </Card>
          ))}
        </Stack>
      )}

      <Modal
        opened={modalOpened}
        onClose={() => setModalOpened(false)}
        title={editing ? `编辑 GitLab 实例 · ${editing.name}` : '添加 GitLab 实例'}
        size="md"
        centered
      >
        <Stack gap="sm">
          <TextInput
            label="实例名称"
            placeholder="例如：公司自建 GitLab"
            value={form.name}
            onChange={(e) => {
              const nextValue = e.currentTarget.value
              setForm((prev) => ({ ...prev, name: nextValue }))
            }}
          />
          <TextInput
            label="GitLab 服务地址"
            required
            placeholder="https://gitlab.company.com 或 http://10.0.0.10:8080"
            description="支持自建域名、内网 IP 及自定义端口，会自动按仓库 git remote 主机名匹配"
            value={form.base_url}
            onChange={(e) => {
              const nextValue = e.currentTarget.value
              setForm((prev) => ({ ...prev, base_url: nextValue }))
            }}
          />
          <PasswordInput
            label="Personal Access Token"
            required={!editing}
            placeholder={editing ? '留空保持当前 Token 不变' : 'glpat-xxxxxxxxxxxxxxxxxxxx'}
            description="只读查看流水线与日志需要 read_api 权限；在星序内重试/取消/手动触发任务需要 api 权限"
            value={form.token}
            onChange={(e) => {
              const nextValue = e.currentTarget.value
              setForm((prev) => ({ ...prev, token: nextValue }))
            }}
          />
          <Switch
            label="跳过 TLS 自签名证书校验（内网自建 HTTPS 证书常用）"
            checked={form.insecure_skip_verify}
            onChange={(e) => {
              const nextChecked = e.currentTarget.checked
              setForm((prev) => ({ ...prev, insecure_skip_verify: nextChecked }))
            }}
          />
          <Group gap="lg">
            <Switch
              label="启用此实例"
              checked={form.enabled}
              onChange={(e) => {
                const nextChecked = e.currentTarget.checked
                setForm((prev) => ({ ...prev, enabled: nextChecked }))
              }}
            />
            <Switch
              label="设为默认实例"
              checked={form.is_default}
              onChange={(e) => {
                const nextChecked = e.currentTarget.checked
                setForm((prev) => ({ ...prev, is_default: nextChecked }))
              }}
            />
          </Group>

          {modalTestNote && (
            <Paper
              p="xs"
              radius="sm"
              withBorder
              style={{
                borderColor: modalTestNote.ok ? 'var(--astr-border)' : 'var(--mantine-color-red-5)',
                background: 'var(--astr-surface-muted)',
              }}
            >
              <Group gap={6} wrap="nowrap">
                {modalTestNote.ok && <IconCheck size={14} color="#2f9e44" />}
                <Text size="xs" c={modalTestNote.ok ? undefined : 'red'}>{modalTestNote.text}</Text>
              </Group>
            </Paper>
          )}

          <Group justify="space-between" mt="sm">
            <Button
              variant="default"
              size="xs"
              leftSection={<IconPlugConnected size={14} />}
              loading={testingModal}
              onClick={() => { void handleTestModal() }}
            >
              测试连接
            </Button>
            <Group gap="xs">
              <Button variant="subtle" color="gray" size="xs" onClick={() => setModalOpened(false)}>
                取消
              </Button>
              <Button color="dark" size="xs" loading={saving} onClick={() => { void handleSave() }}>
                保存配置
              </Button>
            </Group>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
