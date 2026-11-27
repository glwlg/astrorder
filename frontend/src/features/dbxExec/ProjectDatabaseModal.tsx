import { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Divider,
  Group,
  Loader,
  Modal,
  Paper,
  Select,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconDatabase,
  IconPlus,
  IconRefresh,
  IconShieldLock,
  IconStar,
  IconStarFilled,
  IconTrash,
} from '@tabler/icons-react'
import {
  api,
  type DbxConnectionSummary,
  type ProjectDatabaseTarget,
} from '../../api/client'
import { useProjectDbStore } from './projectDbStore'

export interface ProjectDatabaseModalProps {
  opened: boolean
  onClose: () => void
  projectKey: string
  workspaceKey?: string
  projectLabel: string
  onSaved?: (targets: ProjectDatabaseTarget[]) => void
}

function isDbxTargetProd(conn: DbxConnectionSummary | undefined, database: string): boolean {
  if (!conn) return false
  if (conn.is_prod) return true
  const db = database.trim().toLowerCase()
  if (!db) return false
  return (conn.production_databases || []).some((item) => item.trim().toLowerCase() === db)
}

export function ProjectDatabaseModal({
  opened,
  onClose,
  projectKey,
  workspaceKey,
  projectLabel,
  onSaved,
}: ProjectDatabaseModalProps) {
  const connections = useProjectDbStore((s) => s.connections)
  const loadingConnections = useProjectDbStore((s) => s.loadingConnections)
  const connectionsError = useProjectDbStore((s) => s.connectionsError)
  const refreshConnections = useProjectDbStore((s) => s.refreshConnections)
  const hydrateBindings = useProjectDbStore((s) => s.hydrateBindings)
  const findBinding = useProjectDbStore((s) => s.findBinding)
  const saveBinding = useProjectDbStore((s) => s.saveBinding)

  const [targets, setTargets] = useState<ProjectDatabaseTarget[]>([])
  const [saving, setSaving] = useState(false)

  // Add-target draft fields
  const [selectedConnId, setSelectedConnId] = useState<string>('')
  const [databases, setDatabases] = useState<string[]>([])
  const [loadingDbs, setLoadingDbs] = useState(false)
  const [dbLoadError, setDbLoadError] = useState('')
  const [database, setDatabase] = useState('')
  const [schemas, setSchemas] = useState<string[]>([])
  const [loadingSchemas, setLoadingSchemas] = useState(false)
  const [schema, setSchema] = useState('')
  const [aliasLabel, setAliasLabel] = useState('')
  const [manualProd, setManualProd] = useState(false)

  const selectedConn = useMemo(
    () => connections.find((c) => c.id === selectedConnId),
    [connections, selectedConnId],
  )
  const lockedProd = isDbxTargetProd(selectedConn, database)
  const effectiveIsProd = lockedProd || manualProd

  useEffect(() => {
    if (!opened) return
    void hydrateBindings().then(() => {
      const existing = useProjectDbStore.getState().findBinding(projectKey, workspaceKey)
      setTargets(existing ? existing.targets.map((t) => ({ ...t })) : [])
    })
    void refreshConnections()
    setSelectedConnId('')
    setDatabases([])
    setDbLoadError('')
    setDatabase('')
    setSchemas([])
    setSchema('')
    setAliasLabel('')
    setManualProd(false)
  }, [opened, projectKey, workspaceKey, hydrateBindings, refreshConnections])

  // Sync existing targets on first open if bindings were already hydrated
  useEffect(() => {
    if (!opened) return
    const existing = findBinding(projectKey, workspaceKey)
    if (existing) {
      setTargets(existing.targets.map((t) => ({ ...t })))
    }
  }, [opened, projectKey, workspaceKey, findBinding])

  // Load databases when connection changes
  useEffect(() => {
    if (!opened || !selectedConnId) {
      setDatabases([])
      setDbLoadError('')
      setSchemas([])
      return
    }
    const conn = connections.find((c) => c.id === selectedConnId)
    setDatabase(conn?.database || '')
    setSchema(conn?.schema || '')
    setManualProd(Boolean(conn?.is_prod))
    let active = true
    setLoadingDbs(true)
    setDbLoadError('')
    void api
      .listDbxDatabases(selectedConnId)
      .then((res) => {
        if (!active) return
        const list = Array.isArray(res.databases) ? res.databases : []
        setDatabases(list)
        if (!conn?.database && list.length === 1) {
          setDatabase(list[0])
        }
      })
      .catch((err) => {
        if (!active) return
        setDatabases([])
        setDbLoadError(err instanceof Error ? err.message : '读取数据库列表失败，可手动输入库名')
      })
      .finally(() => {
        if (active) setLoadingDbs(false)
      })
    return () => {
      active = false
    }
  }, [opened, selectedConnId, connections])

  // Load schemas when database changes
  useEffect(() => {
    if (!opened || !selectedConnId || !database.trim()) {
      setSchemas([])
      return
    }
    let active = true
    setLoadingSchemas(true)
    void api
      .listDbxSchemas(selectedConnId, database.trim())
      .then((res) => {
        if (!active) return
        setSchemas(Array.isArray(res.schemas) ? res.schemas : [])
      })
      .catch(() => {
        if (active) setSchemas([])
      })
      .finally(() => {
        if (active) setLoadingSchemas(false)
      })
    return () => {
      active = false
    }
  }, [opened, selectedConnId, database])

  const draftTarget = useMemo<ProjectDatabaseTarget | null>(() => {
    if (!selectedConn) return null
    const dbTrimmed = database.trim()
    const schemaTrimmed = schema.trim()
    return {
      id: `${selectedConn.id}::${dbTrimmed}::${schemaTrimmed}`,
      connection_id: selectedConn.id,
      connection_name: selectedConn.name,
      db_type: selectedConn.db_type,
      database: dbTrimmed,
      schema: schemaTrimmed || undefined,
      label: aliasLabel.trim() || undefined,
      is_prod: effectiveIsProd,
      is_default: targets.length === 0,
    }
  }, [aliasLabel, database, effectiveIsProd, schema, selectedConn, targets.length])

  const isDraftDuplicate = useMemo(() => {
    if (!draftTarget) return false
    return targets.some(
      (t) =>
        t.connection_id === draftTarget.connection_id &&
        t.database.trim().toLowerCase() === draftTarget.database.trim().toLowerCase() &&
        (t.schema || '').trim().toLowerCase() === (draftTarget.schema || '').trim().toLowerCase(),
    )
  }, [draftTarget, targets])

  const hasPendingDraft = Boolean(draftTarget && !isDraftDuplicate)

  const resetDraft = () => {
    setSelectedConnId('')
    setDatabases([])
    setDbLoadError('')
    setDatabase('')
    setSchemas([])
    setSchema('')
    setAliasLabel('')
    setManualProd(false)
  }

  const handleAddTarget = () => {
    if (!draftTarget) {
      notifications.show({ color: 'yellow', message: '请先选择一个 DBX 连接' })
      return
    }
    if (isDraftDuplicate) {
      notifications.show({ color: 'yellow', message: '该数据库目标已在当前项目列表中' })
      return
    }
    setTargets((prev) => [...prev, { ...draftTarget, is_default: prev.length === 0 }])
    resetDraft()
  }

  const handleSetDefault = (id: string) => {
    setTargets((prev) => prev.map((t) => ({ ...t, is_default: t.id === id })))
  }

  const handleRemoveTarget = (id: string) => {
    setTargets((prev) => {
      const next = prev.filter((t) => t.id !== id)
      if (next.length > 0 && !next.some((t) => t.is_default)) {
        next[0] = { ...next[0], is_default: true }
      }
      return next
    })
  }

  const handleToggleTargetProd = (id: string) => {
    setTargets((prev) =>
      prev.map((t) => {
        if (t.id !== id) return t
        const conn = connections.find((c) => c.id === t.connection_id)
        if (isDbxTargetProd(conn, t.database)) return { ...t, is_prod: true }
        return { ...t, is_prod: !t.is_prod }
      }),
    )
  }

  const handleSave = async () => {
    if (saving) return
    const nextTargets =
      draftTarget && !isDraftDuplicate
        ? [...targets, { ...draftTarget, is_default: targets.length === 0 }]
        : targets
    if (nextTargets !== targets) {
      setTargets(nextTargets)
      resetDraft()
    }
    setSaving(true)
    try {
      const saved = await saveBinding({
        project_key: projectKey,
        workspace_key: workspaceKey,
        project_label: projectLabel,
        targets: nextTargets,
      })
      notifications.show({
        color: 'teal',
        message:
          saved.targets.length > 0
            ? `已为「${projectLabel}」关联 ${saved.targets.length} 个数据库目标`
            : `已清空「${projectLabel}」的数据库关联`,
      })
      onSaved?.(saved.targets)
      onClose()
    } catch (err) {
      notifications.show({
        color: 'red',
        title: '保存项目数据库关联失败',
        message: err instanceof Error ? err.message : '请稍后重试',
      })
    } finally {
      setSaving(false)
    }
  }

  const connectionOptions = useMemo(
    () =>
      connections.map((c) => ({
        value: c.id,
        label: `${c.name}${c.is_prod ? ' [PROD]' : ''}${c.db_type ? ` (${c.db_type})` : ''}`,
      })),
    [connections],
  )

  return (
    <Modal
      opened={opened}
      onClose={() => {
        if (!saving) onClose()
      }}
      title={
        <Group gap="xs">
          <IconDatabase size={18} style={{ color: 'var(--astr-indigo)' }} />
          <Text fw={650} size="sm">
            关联数据库 · {projectLabel}
          </Text>
        </Group>
      }
      centered
      size="lg"
    >
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          复用本机 DBX 中已保存的连接凭据，绑定后可在该项目的对话中直接执行 SQL 代码块和 .sql 文件（同一代码仓的不同 Agent 与 Worktree 会话自动共享）。
        </Text>

        {connectionsError && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title="未能读取 DBX 连接列表">
            <Stack gap={6}>
              <Text size="xs">{connectionsError}</Text>
              <Button
                size="compact-xs"
                variant="light"
                color="yellow"
                leftSection={<IconRefresh size={13} />}
                loading={loadingConnections}
                onClick={() => void refreshConnections()}
                style={{ alignSelf: 'flex-start' }}
              >
                重试读取 DBX 连接
              </Button>
            </Stack>
          </Alert>
        )}

        <Stack gap={8}>
          <Group justify="space-between">
            <Text size="xs" fw={650} c="dimmed">
              已绑定目标（{targets.length}）
            </Text>
            <Button
              size="compact-xs"
              variant="subtle"
              leftSection={<IconRefresh size={13} />}
              loading={loadingConnections}
              onClick={() => void refreshConnections()}
            >
              刷新 DBX 连接
            </Button>
          </Group>

          {targets.length === 0 ? (
            <Paper withBorder radius="md" p="sm" style={{ background: 'var(--astr-surface-muted)' }}>
              <Text size="xs" c="dimmed" ta="center">
                尚未为该项目关联数据库。从下方选择一个 DBX 连接并添加即可启用对话内直接执行。
              </Text>
            </Paper>
          ) : (
            <Stack gap={6}>
              {targets.map((target) => {
                const conn = connections.find((c) => c.id === target.connection_id)
                const isLockedProd = isDbxTargetProd(conn, target.database)
                return (
                  <Paper key={target.id} withBorder radius="md" px="sm" py={8}>
                    <Group justify="space-between" wrap="nowrap" gap="xs">
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <Tooltip label={target.is_default ? '当前默认目标' : '设为默认目标'}>
                          <ActionIcon
                            size="sm"
                            variant={target.is_default ? 'light' : 'subtle'}
                            color={target.is_default ? 'yellow' : 'gray'}
                            aria-label={target.is_default ? '默认目标' : `将 ${target.connection_name} 设为默认目标`}
                            onClick={() => handleSetDefault(target.id)}
                          >
                            {target.is_default ? <IconStarFilled size={14} /> : <IconStar size={14} />}
                          </ActionIcon>
                        </Tooltip>
                        <div style={{ minWidth: 0, flex: 1 }}>
                          <Group gap={6} wrap="nowrap">
                            <Text size="xs" fw={650} truncate>
                              {target.label || target.connection_name}
                            </Text>
                            {target.db_type && (
                              <Badge size="xs" variant="light" color="gray">
                                {target.db_type}
                              </Badge>
                            )}
                            {target.is_prod && (
                              <Badge size="xs" variant="light" color="red" leftSection={<IconShieldLock size={10} />}>
                                PROD
                              </Badge>
                            )}
                            {target.is_default && (
                              <Badge size="xs" variant="light" color="indigo">
                                默认
                              </Badge>
                            )}
                          </Group>
                          <Text size="xs" c="dimmed" truncate>
                            {target.connection_name}
                            {target.database ? ` / ${target.database}` : ''}
                            {target.schema ? ` · ${target.schema}` : ''}
                          </Text>
                        </div>
                      </Group>
                      <Group gap={6} wrap="nowrap">
                        <Tooltip
                          label={
                            isLockedProd
                              ? 'DBX 中已标记为生产环境，不可降级'
                              : target.is_prod
                                ? '点击取消生产环境标记'
                                : '标记为生产环境（执行前强制弹窗确认）'
                          }
                        >
                          <Button
                            size="compact-xs"
                            variant={target.is_prod ? 'light' : 'subtle'}
                            color={target.is_prod ? 'red' : 'gray'}
                            disabled={isLockedProd}
                            onClick={() => handleToggleTargetProd(target.id)}
                          >
                            {target.is_prod ? 'PROD 保护' : '标为 PROD'}
                          </Button>
                        </Tooltip>
                        <ActionIcon
                          size="sm"
                          variant="subtle"
                          color="red"
                          aria-label={`移除目标 ${target.connection_name}`}
                          onClick={() => handleRemoveTarget(target.id)}
                        >
                          <IconTrash size={14} />
                        </ActionIcon>
                      </Group>
                    </Group>
                  </Paper>
                )
              })}
            </Stack>
          )}
        </Stack>

        <Divider label="添加数据库目标" labelPosition="left" />

        <Stack gap="xs">
          <Select
            label="DBX 连接"
            placeholder={loadingConnections ? '正在读取 DBX 连接…' : '选择已在 DBX 中配置的连接'}
            data={connectionOptions}
            value={selectedConnId || null}
            onChange={(val) => setSelectedConnId(val || '')}
            searchable
            clearable
            rightSection={loadingConnections ? <Loader size={14} /> : undefined}
            comboboxProps={{ withinPortal: true, zIndex: 4000 }}
          />

          <Group grow align="flex-start">
            {databases.length > 0 ? (
              <Select
                label="数据库"
                placeholder="选择目标数据库"
                data={databases}
                value={database || null}
                onChange={(val) => setDatabase(val || '')}
                searchable
                clearable
                rightSection={loadingDbs ? <Loader size={14} /> : undefined}
                comboboxProps={{ withinPortal: true, zIndex: 4000 }}
              />
            ) : (
              <TextInput
                label="数据库"
                placeholder={loadingDbs ? '正在加载库列表…' : '输入或留空使用连接默认库'}
                value={database}
                onChange={(e) => setDatabase(e.currentTarget.value)}
                description={dbLoadError || undefined}
                rightSection={loadingDbs ? <Loader size={14} /> : undefined}
              />
            )}

            {schemas.length > 0 ? (
              <Select
                label="Schema（可选）"
                placeholder="留空使用默认 Schema"
                data={schemas}
                value={schema || null}
                onChange={(val) => setSchema(val || '')}
                searchable
                clearable
                rightSection={loadingSchemas ? <Loader size={14} /> : undefined}
                comboboxProps={{ withinPortal: true, zIndex: 4000 }}
              />
            ) : (
              <TextInput
                label="Schema（可选）"
                placeholder="如 public"
                value={schema}
                onChange={(e) => setSchema(e.currentTarget.value)}
                rightSection={loadingSchemas ? <Loader size={14} /> : undefined}
              />
            )}
          </Group>

          <Group align="flex-end" justify="space-between" wrap="wrap">
            <TextInput
              label="显示别名（可选）"
              placeholder="如：线上元数据 / 本地开发库"
              value={aliasLabel}
              onChange={(e) => setAliasLabel(e.currentTarget.value)}
              style={{ flex: 1, minWidth: 180 }}
            />
            <Checkbox
              label={lockedProd ? '生产环境 PROD（由 DBX 锁定）' : '标记为生产环境（PROD）'}
              checked={effectiveIsProd}
              disabled={lockedProd}
              onChange={(e) => setManualProd(e.currentTarget.checked)}
              mb={6}
            />
            <Button
              size="xs"
              variant="light"
              color="indigo"
              leftSection={<IconPlus size={14} />}
              disabled={!selectedConnId}
              onClick={handleAddTarget}
              mb={2}
            >
              添加到项目
            </Button>
          </Group>
        </Stack>

        <Group justify="space-between" align="center" mt="xs" wrap="wrap">
          <Text size="xs" c="dimmed">
            {hasPendingDraft && draftTarget
              ? `直接点击「保存关联」将自动加入「${draftTarget.label || draftTarget.database || draftTarget.connection_name}」`
              : ''}
          </Text>
          <Group justify="flex-end" gap="xs">
            <Button variant="subtle" color="gray" disabled={saving} onClick={onClose}>
              取消
            </Button>
            <Button color="indigo" loading={saving} onClick={() => void handleSave()}>
              保存关联
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
