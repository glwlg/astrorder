import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  NativeSelect,
  NumberInput,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import {
  IconAlertCircle,
  IconArrowUp,
  IconFile,
  IconFolder,
  IconFolderSearch,
} from '@tabler/icons-react'
import {
  api,
  ApiError,
  type BackupBrowseResponse,
  type BackupJob,
  type BackupJobPayload,
  type BackupRestoreAgentTarget,
  type BackupTarget,
} from '../../api/client'
import { BackupRestoreHookFields, type RestoreHookMode } from './BackupRestoreHookFields'

interface BackupDesktopBridge {
  openDirectoryDialog?: (defaultPath?: string) => Promise<string>
  openFileDialog?: (defaultPath?: string) => Promise<string>
}

function getDesktopBridge(): BackupDesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as unknown as { astrorderDesktop?: BackupDesktopBridge }).astrorderDesktop
}

const WEEKDAY_OPTIONS = [
  { value: '1', label: '每周一' },
  { value: '2', label: '每周二' },
  { value: '3', label: '每周三' },
  { value: '4', label: '每周四' },
  { value: '5', label: '每周五' },
  { value: '6', label: '每周六' },
  { value: '0', label: '每周日' },
]

const INTERVAL_PRESETS = [
  { label: '15 分钟', value: 15 },
  { label: '30 分钟', value: 30 },
  { label: '1 小时', value: 60 },
  { label: '6 小时', value: 360 },
  { label: '12 小时', value: 720 },
  { label: '24 小时', value: 1440 },
]

function targetKindLabel(kind: string): string {
  if (kind === 'local') return '本机'
  if (kind === 'wsl') return 'WSL'
  return 'SSH'
}

export function BackupJobModal({
  opened,
  onClose,
  job,
  cloning,
  targets,
  defaultDestinationDir,
  submitting,
  errorMessage,
  onSubmit,
}: {
  opened: boolean
  onClose: () => void
  job: BackupJob | null
  cloning: boolean
  targets: BackupTarget[]
  defaultDestinationDir: string
  submitting: boolean
  errorMessage: string | null
  onSubmit: (payload: BackupJobPayload, copyVersions: boolean) => void
}) {
  const [name, setName] = useState('')
  const [connectionId, setConnectionId] = useState('local')
  const [sourcePath, setSourcePath] = useState('')
  const [destinationDir, setDestinationDir] = useState('')
  const [postRestoreCommand, setPostRestoreCommand] = useState('')
  const [restoreHookMode, setRestoreHookMode] = useState<RestoreHookMode>('none')
  const [restoreAgent, setRestoreAgent] = useState<BackupRestoreAgentTarget | null>(null)
  const [restoreAgentError, setRestoreAgentError] = useState<string | null>(null)
  const restoreAgentInput = useRef<HTMLSelectElement>(null)
  const [scheduleEnabled, setScheduleEnabled] = useState(false)
  const [scheduleMode, setScheduleMode] = useState<string>('manual')
  const [intervalMinutes, setIntervalMinutes] = useState<number>(60)
  const [dailyTime, setDailyTime] = useState('03:00')
  const [weekday, setWeekday] = useState<number>(1)
  const [retentionCount, setRetentionCount] = useState<number>(20)
  const [copyVersions, setCopyVersions] = useState(false)
  const [localValidationError, setLocalValidationError] = useState<string | null>(null)
  const sourceInput = useRef<HTMLInputElement>(null)
  const destinationInput = useRef<HTMLInputElement>(null)

  const [browserTarget, setBrowserTarget] = useState<'source' | 'destination' | null>(null)
  const [browserConnId, setBrowserConnId] = useState('local')
  const [browserInputPath, setBrowserInputPath] = useState('')
  const [browserData, setBrowserData] = useState<BackupBrowseResponse | null>(null)
  const [browserLoading, setBrowserLoading] = useState(false)
  const [browserError, setBrowserError] = useState<string | null>(null)

  const desktop = getDesktopBridge()

  useEffect(() => {
    if (!opened) return
    setLocalValidationError(null)
    setRestoreAgentError(null)
    setCopyVersions(false)
    if (job) {
      setName(cloning ? `${Array.from(job.name).slice(0, 117).join('')} 副本` : job.name)
      setConnectionId(job.connection_id || 'local')
      setSourcePath(job.source_path)
      setDestinationDir(job.destination_dir)
      setPostRestoreCommand(job.post_restore_command || '')
      setRestoreAgent(job.post_restore_restart_agent ?? null)
      setRestoreHookMode(job.post_restore_restart_agent ? 'restart_agent' : job.post_restore_command ? 'command' : 'none')
      setScheduleEnabled(job.schedule_enabled)
      setScheduleMode(job.schedule_mode || 'manual')
      setIntervalMinutes(job.interval_minutes > 0 ? job.interval_minutes : 60)
      setDailyTime(job.daily_time || '03:00')
      setWeekday(job.weekday >= 0 && job.weekday <= 6 ? job.weekday : 1)
      setRetentionCount(job.retention_count >= 0 ? job.retention_count : 20)
    } else {
      setName('')
      setConnectionId(targets[0]?.id || 'local')
      setSourcePath('')
      setDestinationDir(defaultDestinationDir || '')
      setPostRestoreCommand('')
      setRestoreAgent(null)
      setRestoreHookMode('none')
      setScheduleEnabled(false)
      setScheduleMode('manual')
      setIntervalMinutes(60)
      setDailyTime('03:00')
      setWeekday(1)
      setRetentionCount(20)
    }
    // Only re-initialize when modal opens or the edited job changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened, job?.id, cloning])

  useEffect(() => {
    if (opened && !job && !destinationDir && defaultDestinationDir) {
      setDestinationDir(defaultDestinationDir)
    }
  }, [opened, job, destinationDir, defaultDestinationDir])

  const targetOptions = useMemo(() => {
    const items = targets.map(t => ({
      value: t.id,
      label: `${t.name} (${targetKindLabel(t.kind)}${t.state === 'connected' ? ' · 在线' : ' · 离线'})`,
    }))
    if (connectionId && !items.some(item => item.value === connectionId)) {
      items.push({ value: connectionId, label: job?.target_name || connectionId })
    }
    if (items.length === 0) {
      items.push({ value: 'local', label: '本机 (本机)' })
    }
    return items
  }, [targets, connectionId, job])

  async function loadBrowse(connId: string, nextPath?: string) {
    setBrowserLoading(true)
    setBrowserError(null)
    try {
      const res = await api.browseBackupPath(connId, nextPath || undefined)
      setBrowserData(res)
      setBrowserInputPath(res.root)
    } catch (err) {
      const msg = err instanceof ApiError ? err.detail : err instanceof Error ? err.message : '读取目录失败'
      setBrowserError(msg)
    } finally {
      setBrowserLoading(false)
    }
  }

  function openBrowser(mode: 'source' | 'destination') {
    const conn = mode === 'destination' ? 'local' : connectionId
    const initial = mode === 'destination' ? destinationDir.trim() : sourcePath.trim()
    setBrowserTarget(mode)
    setBrowserConnId(conn)
    setBrowserData(null)
    setBrowserError(null)
    setBrowserInputPath(initial)
    void loadBrowse(conn, initial)
  }

  async function pickNativeDirectory(forField: 'source' | 'destination') {
    if (!desktop?.openDirectoryDialog) return
    try {
      const current = forField === 'source' ? sourcePath : destinationDir
      const chosen = await desktop.openDirectoryDialog(current)
      if (chosen) {
        if (forField === 'source') setSourcePath(chosen)
        else setDestinationDir(chosen)
      }
    } catch {}
  }

  async function pickNativeSourceFile() {
    if (!desktop?.openFileDialog) return
    try {
      const chosen = await desktop.openFileDialog(sourcePath)
      if (chosen) setSourcePath(chosen)
    } catch {}
  }

  function handleScheduleSwitch(checked: boolean) {
    setScheduleEnabled(checked)
    if (checked && scheduleMode === 'manual') {
      setScheduleMode('interval')
    }
    if (!checked && scheduleMode !== 'manual') {
      // keep chosen mode for convenience or let toggle pause it cleanly
    }
  }

  function handleScheduleModeChange(val: string | null) {
    const next = val || 'manual'
    setScheduleMode(next)
    if (next === 'manual') {
      setScheduleEnabled(false)
    } else {
      setScheduleEnabled(true)
    }
  }

  function handleSave() {
    setLocalValidationError(null)
    if (!sourcePath.trim()) {
      setLocalValidationError('请填写需要备份的源目录或文件路径。')
      sourceInput.current?.focus()
      return
    }
    if (!destinationDir.trim()) {
      setLocalValidationError('请填写本机备份保存目录。')
      destinationInput.current?.focus()
      return
    }
    if (restoreHookMode === 'restart_agent' && !restoreAgent) {
      setRestoreAgentError('请选择要重启的 Agent。')
      restoreAgentInput.current?.focus()
      return
    }
    onSubmit({
      name: name.trim(),
      connection_id: connectionId || 'local',
      source_path: sourcePath.trim(),
      destination_dir: destinationDir.trim(),
      post_restore_command: restoreHookMode === 'command' ? postRestoreCommand : '',
      post_restore_restart_agent: restoreHookMode === 'restart_agent' ? restoreAgent : null,
      schedule_enabled: scheduleMode === 'manual' ? false : scheduleEnabled,
      schedule_mode: scheduleMode,
      interval_minutes: intervalMinutes,
      daily_time: dailyTime.trim() || '03:00',
      weekday,
      retention_count: retentionCount,
    }, cloning && copyVersions)
  }

  const combinedError = localValidationError || errorMessage

  return (
    <>
      <Modal
        opened={opened}
        onClose={onClose}
        title={cloning ? '克隆备份任务' : job ? '编辑备份任务' : '新建备份任务'}
        size="lg"
        centered
        closeOnEscape={!submitting}
        closeOnClickOutside={!submitting}
        withCloseButton={!submitting}
      >
        <Stack gap="sm">
          {combinedError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} variant="light">
              {combinedError}
            </Alert>
          )}

          <TextInput
            label="任务名称"
            placeholder="留空将自动使用源目录/文件名"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
          />

          <NativeSelect
            label="源服务器"
            name="connection_id"
            data={targetOptions}
            value={connectionId}
            onChange={(event) => {
              const v = event.currentTarget.value
              if (v && v !== connectionId) {
                setConnectionId(v)
                if (cloning) {
                  setSourcePath('')
                  setLocalValidationError(null)
                }
              }
            }}
            disabled={submitting}
          />

          <Stack gap={6}>
            <TextInput
              ref={sourceInput}
              label="源目录或文件路径"
              description={cloning ? '克隆的版本将还原到此服务器上的路径。' : '输入源服务器上的绝对路径，或点击下方按钮浏览。'}
              placeholder={connectionId === 'local' ? '例如：D:\\Projects\\app 或 /srv/app/config.yaml' : '例如：/home/luwei/workspace/demo'}
              value={sourcePath}
              onChange={(e) => { setSourcePath(e.currentTarget.value); setLocalValidationError(null) }}
              required
            />
            <Group gap="xs">
              <Button size="xs" variant="light" leftSection={<IconFolderSearch size={14} />} onClick={() => openBrowser('source')}>
                浏览服务器路径
              </Button>
              {connectionId === 'local' && desktop?.openDirectoryDialog && (
                <Button size="xs" variant="default" leftSection={<IconFolder size={14} />} onClick={() => void pickNativeDirectory('source')}>
                  选择本机目录
                </Button>
              )}
              {connectionId === 'local' && desktop?.openFileDialog && (
                <Button size="xs" variant="default" leftSection={<IconFile size={14} />} onClick={() => void pickNativeSourceFile()}>
                  选择本机文件
                </Button>
              )}
            </Group>
          </Stack>

          <Stack gap={6}>
            <TextInput
              ref={destinationInput}
              label="本机备份保存目录"
              description="每次备份都会在此目录下按任务和版本号建立独立快照文件夹。"
              placeholder="例如：D:\\AstrorderBackups"
              value={destinationDir}
              onChange={(e) => { setDestinationDir(e.currentTarget.value); setLocalValidationError(null) }}
              required
            />
            <Group gap="xs">
              <Button size="xs" variant="light" leftSection={<IconFolderSearch size={14} />} onClick={() => openBrowser('destination')}>
                浏览本机目录
              </Button>
              {desktop?.openDirectoryDialog && (
                <Button size="xs" variant="default" leftSection={<IconFolder size={14} />} onClick={() => void pickNativeDirectory('destination')}>
                  选择本机目录
                </Button>
              )}
            </Group>
          </Stack>

          <Group justify="space-between" align="center" mt="xs">
            <Switch
              label="启用定时自动备份"
              checked={scheduleEnabled && scheduleMode !== 'manual'}
              onChange={(e) => handleScheduleSwitch(e.currentTarget.checked)}
            />
          </Group>

          <Select
            label="备份策略"
            value={scheduleMode}
            onChange={handleScheduleModeChange}
            allowDeselect={false}
            data={[
              { value: 'manual', label: '仅手动备份' },
              { value: 'interval', label: '按固定时间间隔' },
              { value: 'daily', label: '每天定时' },
              { value: 'weekly', label: '每周定时' },
            ]}
          />

          {scheduleMode === 'interval' && (
            <Stack gap="xs">
              <Group gap={6} wrap="wrap">
                {INTERVAL_PRESETS.map((preset) => (
                  <Button
                    key={preset.value}
                    size="compact-xs"
                    variant={intervalMinutes === preset.value ? 'filled' : 'light'}
                    onClick={() => setIntervalMinutes(preset.value)}
                  >
                    {preset.label}
                  </Button>
                ))}
              </Group>
              <NumberInput
                label="间隔分钟数"
                min={5}
                max={43200}
                value={intervalMinutes}
                onChange={(val) => setIntervalMinutes(typeof val === 'number' ? val : 60)}
              />
            </Stack>
          )}

          {scheduleMode === 'daily' && (
            <TextInput
              label="每天执行时间 (HH:MM)"
              placeholder="03:00"
              value={dailyTime}
              onChange={(e) => setDailyTime(e.currentTarget.value)}
            />
          )}

          {scheduleMode === 'weekly' && (
            <Group grow>
              <Select
                label="星期"
                data={WEEKDAY_OPTIONS}
                value={String(weekday)}
                onChange={(val) => setWeekday(Number(val ?? '1'))}
                allowDeselect={false}
              />
              <TextInput
                label="执行时间 (HH:MM)"
                placeholder="03:00"
                value={dailyTime}
                onChange={(e) => setDailyTime(e.currentTarget.value)}
              />
            </Group>
          )}

          <NumberInput
            label="保留历史版本数（0 表示不限制）"
            description="超过该数量的旧成功备份版本将在新备份完成后自动清理。"
            min={0}
            max={500}
            value={retentionCount}
            onChange={(val) => setRetentionCount(typeof val === 'number' ? val : 20)}
          />

          <BackupRestoreHookFields
            opened={opened}
            mode={restoreHookMode}
            onModeChange={(mode) => { setRestoreHookMode(mode); setRestoreAgentError(null) }}
            command={postRestoreCommand}
            onCommandChange={setPostRestoreCommand}
            agent={restoreAgent}
            onAgentChange={(agent) => { setRestoreAgent(agent); setRestoreAgentError(null) }}
            agentInput={restoreAgentInput}
            agentError={restoreAgentError}
          />

          {cloning && (
            <Checkbox
              name="copy_versions"
              label="同时克隆成功的备份版本"
              description="复制快照文件，之后可在新任务的版本历史中还原。"
              checked={copyVersions}
              onChange={(event) => setCopyVersions(event.currentTarget.checked)}
              disabled={submitting || job?.version_count === 0}
            />
          )}

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={onClose} disabled={submitting}>取消</Button>
            <Button onClick={handleSave} loading={submitting}>
              {cloning ? (submitting ? '正在克隆…' : '克隆任务') : job ? '保存修改' : '创建任务'}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={browserTarget !== null}
        onClose={() => setBrowserTarget(null)}
        title={browserTarget === 'destination' ? '浏览本机保存目录' : '浏览服务器目录/文件'}
        size="lg"
        centered
      >
        <Stack gap="sm">
          <Group gap="xs" align="flex-end">
            <TextInput
              style={{ flex: 1 }}
              label="当前路径"
              value={browserInputPath}
              onChange={(e) => setBrowserInputPath(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  void loadBrowse(browserConnId, browserInputPath.trim())
                }
              }}
            />
            <Button variant="light" loading={browserLoading} onClick={() => void loadBrowse(browserConnId, browserInputPath.trim())}>
              跳转
            </Button>
          </Group>

          {browserData?.drives && browserData.drives.length > 0 && (
            <Group gap={6} wrap="wrap">
              {browserData.parent && (
                <Button
                  size="compact-xs"
                  variant="default"
                  leftSection={<IconArrowUp size={13} />}
                  onClick={() => void loadBrowse(browserConnId, browserData.parent || undefined)}
                >
                  上级目录
                </Button>
              )}
              {browserData.drives.map((drive) => (
                <Button key={drive} size="compact-xs" variant="light" onClick={() => void loadBrowse(browserConnId, drive)}>
                  {drive}
                </Button>
              ))}
            </Group>
          )}

          {browserError && (
            <Alert color="red" variant="light" icon={<IconAlertCircle size={16} />}>
              {browserError}
            </Alert>
          )}

          <div className="backup-browse-list">
            {browserLoading ? (
              <Text size="sm" c="dimmed" p="md">正在读取目录…</Text>
            ) : !browserData || browserData.items.length === 0 ? (
              <Text size="sm" c="dimmed" p="md">当前目录为空或无可浏览条目。</Text>
            ) : (
              browserData.items.map((item) => (
                <div
                  key={item.path}
                  className="backup-browse-row"
                  onClick={() => {
                    if (item.is_dir) {
                      void loadBrowse(browserConnId, item.path)
                    } else if (browserTarget === 'source') {
                      setSourcePath(item.path)
                      setBrowserTarget(null)
                    }
                  }}
                >
                  <Group gap={8} style={{ minWidth: 0 }}>
                    {item.is_dir ? <IconFolder size={16} /> : <IconFile size={16} />}
                    <Text size="sm" truncate>{item.name}</Text>
                  </Group>
                  <Group gap={6}>
                    {item.is_dir ? (
                      <Badge size="xs" variant="light">目录</Badge>
                    ) : browserTarget === 'source' ? (
                      <Badge size="xs" variant="outline">点击选择文件</Badge>
                    ) : null}
                  </Group>
                </div>
              ))
            )}
          </div>

          <Group justify="space-between" mt="xs">
            <Text size="xs" c="dimmed">提示：点击文件夹可进入；点击文件可直接选中。</Text>
            <Group gap="xs">
              <Button variant="default" onClick={() => setBrowserTarget(null)}>关闭</Button>
              {browserData?.root && (
                <Button
                  onClick={() => {
                    if (browserTarget === 'destination') {
                      setDestinationDir(browserData.root)
                    } else {
                      setSourcePath(browserData.root)
                    }
                    setBrowserTarget(null)
                  }}
                >
                  选择当前目录
                </Button>
              )}
            </Group>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}
