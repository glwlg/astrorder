import { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  IconAlertCircle,
  IconCheck,
  IconClock,
  IconCopy,
  IconEdit,
  IconFolderOpen,
  IconPlayerPlay,
  IconPlus,
  IconRefresh,
  IconServer,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import {
  api,
  ApiError,
  type BackupJob,
  type BackupJobPayload,
  type BackupVersion,
} from '../../api/client'
import { BackupJobModal } from './BackupJobModal'
import { BackupRestoreModal } from './BackupRestoreModal'
import './BackupsPage.css'

const WEEKDAY_NAMES = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let idx = 0
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024
    idx += 1
  }
  return `${idx === 0 ? value.toFixed(0) : value.toFixed(value >= 100 ? 0 : 1)} ${units[idx]}`
}

function formatDateTime(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('zh-CN', { hour12: false })
}

function formatScheduleSummary(job: BackupJob): string {
  if (job.schedule_mode === 'manual') return '仅手动'
  let label = ''
  if (job.schedule_mode === 'interval') {
    if (job.interval_minutes >= 60 && job.interval_minutes % 60 === 0) {
      label = `每 ${job.interval_minutes / 60} 小时`
    } else {
      label = `每 ${job.interval_minutes} 分钟`
    }
  } else if (job.schedule_mode === 'daily') {
    label = `每天 ${job.daily_time}`
  } else if (job.schedule_mode === 'weekly') {
    const dayName = WEEKDAY_NAMES[job.weekday] ?? '每周一'
    label = `每${dayName} ${job.daily_time}`
  } else {
    label = job.schedule_mode
  }
  return job.schedule_enabled ? label : `${label}（已暂停）`
}

function statusBadge(status?: string | null) {
  switch (status) {
    case 'running':
      return { color: 'blue', label: '备份中…' }
    case 'success':
      return { color: 'teal', label: '成功' }
    case 'failed':
      return { color: 'red', label: '失败' }
    default:
      return { color: 'gray', label: '未执行' }
  }
}

function targetKindText(kind: string): string {
  if (kind === 'local') return '本机'
  if (kind === 'wsl') return 'WSL'
  return 'SSH'
}

export function BackupsPage() {
  const queryClient = useQueryClient()
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null)
  const [jobForm, setJobForm] = useState<{ mode: 'create' } | { mode: 'edit' | 'clone'; job: BackupJob } | null>(null)
  const [jobFormError, setJobFormError] = useState<string | null>(null)

  const [runModalJob, setRunModalJob] = useState<BackupJob | null>(null)
  const [runNote, setRunNote] = useState('')
  const [restoreVersion, setRestoreVersion] = useState<BackupVersion | null>(null)

  const [editingVersionId, setEditingVersionId] = useState<string | null>(null)
  const [editingNoteText, setEditingNoteText] = useState('')

  const [deleteTarget, setDeleteTarget] = useState<
    | { kind: 'job'; item: BackupJob }
    | { kind: 'version'; item: BackupVersion }
    | null
  >(null)
  const [deleteFilesChecked, setDeleteFilesChecked] = useState(true)

  const targetsQuery = useQuery({
    queryKey: ['astrorder', 'backups', 'targets'],
    queryFn: () => api.listBackupTargets(),
  })

  const jobsQuery = useQuery({
    queryKey: ['astrorder', 'backups', 'jobs'],
    queryFn: () => api.listBackupJobs(),
    refetchInterval: (query) => {
      const jobs = query.state.data?.items || []
      return jobs.some(j => j.is_running || j.last_status === 'running') ? 2000 : false
    },
  })

  const jobs = jobsQuery.data?.items || []

  useEffect(() => {
    if (jobs.length === 0) {
      setSelectedJobId(null)
      return
    }
    if (!selectedJobId || !jobs.some(j => j.id === selectedJobId)) {
      setSelectedJobId(jobs[0].id)
    }
  }, [jobs, selectedJobId])

  const selectedJob = useMemo(
    () => jobs.find(j => j.id === selectedJobId) || null,
    [jobs, selectedJobId],
  )

  const versionsQuery = useQuery({
    queryKey: ['astrorder', 'backups', 'versions', selectedJobId],
    queryFn: () => api.listBackupVersions(selectedJobId!),
    enabled: Boolean(selectedJobId),
    refetchInterval: (query) => {
      const items = query.state.data?.items || []
      const hasRunning = items.some(v => v.status === 'running') || Boolean(selectedJob?.is_running)
      return hasRunning ? 2000 : false
    },
  })

  const versions = versionsQuery.data?.items || []

  function invalidateAll() {
    void queryClient.invalidateQueries({ queryKey: ['astrorder', 'backups'] })
  }

  const saveJobMutation = useMutation({
    mutationFn: async ({ payload, copyVersions }: { payload: BackupJobPayload; copyVersions: boolean }) => {
      if (jobForm?.mode === 'clone') {
        return api.cloneBackupJob(jobForm.job.id, { ...payload, copy_versions: copyVersions })
      }
      if (jobForm?.mode === 'edit') {
        return api.updateBackupJob(jobForm.job.id, payload)
      }
      return api.createBackupJob(payload)
    },
    onSuccess: (saved) => {
      setJobFormError(null)
      setJobForm(null)
      queryClient.setQueryData<{ items: BackupJob[] }>(['astrorder', 'backups', 'jobs'], (old) => ({
        items: old?.items.some((item) => item.id === saved.id)
          ? old.items.map((item) => item.id === saved.id ? saved : item)
          : [saved, ...(old?.items ?? [])],
      }))
      setSelectedJobId(saved.id)
      invalidateAll()
      notifications.show({
        title: jobForm?.mode === 'clone' ? '备份任务已克隆' : jobForm?.mode === 'edit' ? '备份任务已更新' : '备份任务已创建',
        message: jobForm?.mode === 'clone' && saved.version_count > 0
          ? `已复制 ${saved.version_count} 个版本，可在新任务的版本历史中还原。`
          : saved.name,
        color: 'teal',
      })
    },
    onError: (err) => {
      setJobFormError(err instanceof ApiError ? err.detail : err instanceof Error ? err.message : '保存失败')
    },
  })

  const toggleScheduleMutation = useMutation({
    mutationFn: async ({ job, enabled }: { job: BackupJob; enabled: boolean }) => {
      const mode = enabled && job.schedule_mode === 'manual' ? 'interval' : job.schedule_mode
      return api.updateBackupJob(job.id, {
        name: job.name,
        connection_id: job.connection_id,
        source_path: job.source_path,
        destination_dir: job.destination_dir,
        schedule_enabled: enabled,
        schedule_mode: mode,
        interval_minutes: job.interval_minutes || 60,
        daily_time: job.daily_time || '03:00',
        weekday: job.weekday,
        retention_count: job.retention_count,
      })
    },
    onSuccess: () => invalidateAll(),
    onError: (err) => {
      notifications.show({
        title: '切换定时状态失败',
        message: err instanceof ApiError ? err.detail : '请求失败',
        color: 'red',
      })
    },
  })

  const runJobMutation = useMutation({
    mutationFn: async ({ jobId, note }: { jobId: string; note: string }) => api.runBackupJob(jobId, note),
    onSuccess: () => {
      setRunModalJob(null)
      setRunNote('')
      invalidateAll()
      notifications.show({ title: '已开始备份', message: '正在后台执行快照创建…', color: 'blue' })
    },
    onError: (err) => {
      notifications.show({
        title: '启动备份失败',
        message: err instanceof ApiError ? err.detail : '请求失败',
        color: 'red',
      })
    },
  })

  const updateNoteMutation = useMutation({
    mutationFn: async ({ versionId, note }: { versionId: string; note: string }) =>
      api.updateBackupVersionNote(versionId, note),
    onSuccess: () => {
      setEditingVersionId(null)
      setEditingNoteText('')
      invalidateAll()
    },
    onError: (err) => {
      notifications.show({
        title: '更新备注失败',
        message: err instanceof ApiError ? err.detail : '请求失败',
        color: 'red',
      })
    },
  })

  const openFolderMutation = useMutation({
    mutationFn: async (versionId: string) => api.openBackupVersionFolder(versionId),
    onSuccess: (res) => {
      notifications.show({ title: '已打开本机目录', message: res.path, color: 'teal' })
    },
    onError: (err) => {
      notifications.show({
        title: '打开目录失败',
        message: err instanceof ApiError ? err.detail : '无法打开目录',
        color: 'red',
      })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async () => {
      if (!deleteTarget) return
      if (deleteTarget.kind === 'job') {
        await api.deleteBackupJob(deleteTarget.item.id, deleteFilesChecked)
      } else {
        await api.deleteBackupVersion(deleteTarget.item.id, deleteFilesChecked)
      }
    },
    onSuccess: () => {
      setDeleteTarget(null)
      invalidateAll()
      notifications.show({ title: '删除成功', message: '已清理选定记录。', color: 'teal' })
    },
    onError: (err) => {
      notifications.show({
        title: '删除失败',
        message: err instanceof ApiError ? err.detail : '请求失败',
        color: 'red',
      })
    },
  })

  function openCreateModal() {
    setJobFormError(null)
    setJobForm({ mode: 'create' })
  }

  function openEditModal(job: BackupJob) {
    setJobFormError(null)
    setJobForm({ mode: 'edit', job })
  }

  function openCloneModal(job: BackupJob) {
    setJobFormError(null)
    setJobForm({ mode: 'clone', job })
  }

  return (
    <div className="route-page backups-page">
      <div className="backups-header">
        <div>
          <Title order={2} size="h3">文件与目录备份</Title>
          <Text size="sm" c="dimmed">
            将本机或已连接 SSH / WSL 服务器上的指定目录或文件备份到本机版本库，支持手动/定时备份与版本备注。
          </Text>
        </div>
        <Group gap="xs">
          <Button
            variant="default"
            leftSection={<IconRefresh size={16} />}
            loading={jobsQuery.isFetching || versionsQuery.isFetching}
            onClick={invalidateAll}
          >
            刷新
          </Button>
          <Button leftSection={<IconPlus size={16} />} onClick={openCreateModal}>
            新建备份任务
          </Button>
        </Group>
      </div>

      {jobsQuery.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />}>
          加载备份任务失败：{jobsQuery.error instanceof Error ? jobsQuery.error.message : '未知错误'}
        </Alert>
      )}

      <div className="backups-layout">
        <section className="backups-pane" aria-label="备份任务列表">
          <div className="backups-pane-header">
            <Text fw={600} size="sm">备份任务 ({jobs.length})</Text>
          </div>
          <div className="backups-pane-scroll">
            {jobs.length === 0 ? (
              <Stack align="center" py="xl" gap="xs">
                <Text size="sm" c="dimmed">还没有创建任何备份任务</Text>
                <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={openCreateModal}>
                  新建第一个备份任务
                </Button>
              </Stack>
            ) : (
              jobs.map((job) => {
                const selected = job.id === selectedJobId
                const badge = statusBadge(job.is_running ? 'running' : job.last_status)
                return (
                  <div
                    key={job.id}
                    className={`backup-job-card ${selected ? 'is-selected' : ''}`}
                    onClick={() => setSelectedJobId(job.id)}
                  >
                    <Group justify="space-between" wrap="nowrap" gap={8}>
                      <Group gap={6} style={{ minWidth: 0 }}>
                        <Text fw={600} size="sm" truncate>{job.name}</Text>
                        <Badge size="xs" variant="light" color="gray">
                          {job.target_name || job.connection_id} · {targetKindText(job.target_kind)}
                        </Badge>
                      </Group>
                      <Badge size="xs" color={badge.color} variant="dot">
                        {badge.label}
                      </Badge>
                    </Group>

                    <div className="backup-path-line">
                      <div>源：{job.source_path}</div>
                      <div>存：{job.destination_dir}</div>
                    </div>

                    <Group justify="space-between" align="center" gap="xs">
                      <Group gap={6}>
                        <Badge size="xs" variant="outline" leftSection={<IconClock size={11} />}>
                          {formatScheduleSummary(job)}
                        </Badge>
                        <Badge size="xs" variant="light">
                          {job.version_count} 个版本
                        </Badge>
                      </Group>

                      <Group gap={4} onClick={(e) => e.stopPropagation()}>
                        <Switch
                          size="xs"
                          aria-label={`启用 ${job.name} 定时备份`}
                          checked={job.schedule_enabled && job.schedule_mode !== 'manual'}
                          onChange={(e) =>
                            toggleScheduleMutation.mutate({ job, enabled: e.currentTarget.checked })
                          }
                        />
                        <Tooltip label="立即备份">
                          <ActionIcon
                            variant="light"
                            color="blue"
                            size="sm"
                            aria-label={`立即备份 ${job.name}`}
                            disabled={job.is_running}
                            onClick={() => {
                              setRunNote('')
                              setRunModalJob(job)
                            }}
                          >
                            <IconPlayerPlay size={14} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="编辑任务">
                          <ActionIcon
                            variant="subtle"
                            size="sm"
                            aria-label={`编辑 ${job.name}`}
                            onClick={() => openEditModal(job)}
                          >
                            <IconEdit size={14} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="克隆任务">
                          <ActionIcon
                            variant="subtle"
                            size="sm"
                            aria-label={`克隆 ${job.name}`}
                            disabled={job.is_running}
                            onClick={() => openCloneModal(job)}
                          >
                            <IconCopy size={14} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="删除任务">
                          <ActionIcon
                            variant="subtle"
                            color="red"
                            size="sm"
                            aria-label={`删除 ${job.name}`}
                            disabled={job.is_running}
                            onClick={() => {
                              setDeleteFilesChecked(true)
                              setDeleteTarget({ kind: 'job', item: job })
                            }}
                          >
                            <IconTrash size={14} />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </Group>
                  </div>
                )
              })
            )}
          </div>
        </section>

        <section className="backups-pane" aria-label="版本历史详情">
          {!selectedJob ? (
            <Stack align="center" justify="center" style={{ flex: 1 }} p="xl">
              <Text size="sm" c="dimmed">请在左侧选择一个备份任务查看版本历史，或点击右上角新建任务。</Text>
            </Stack>
          ) : (
            <>
              <div className="backups-pane-header">
                <Group gap="xs" style={{ minWidth: 0 }}>
                  <IconServer size={18} />
                  <Text fw={600} size="md" truncate>{selectedJob.name}</Text>
                  <Badge size="sm" variant="light">
                    {selectedJob.target_name || selectedJob.connection_id}
                  </Badge>
                </Group>
                <Group gap="xs">
                  <Button
                    size="xs"
                    variant="default"
                    leftSection={<IconEdit size={14} />}
                    onClick={() => openEditModal(selectedJob)}
                  >
                    编辑配置
                  </Button>
                  <Button
                    size="xs"
                    variant="default"
                    leftSection={<IconCopy size={14} />}
                    disabled={selectedJob.is_running}
                    onClick={() => openCloneModal(selectedJob)}
                  >
                    克隆任务
                  </Button>
                  <Button
                    size="xs"
                    leftSection={<IconPlayerPlay size={14} />}
                    loading={selectedJob.is_running}
                    onClick={() => {
                      setRunNote('')
                      setRunModalJob(selectedJob)
                    }}
                  >
                    立即备份
                  </Button>
                </Group>
              </div>

              <div className="backup-detail-meta">
                <div className="backup-meta-cell">
                  <span className="backup-meta-label">源路径</span>
                  <span className="backup-meta-value backup-meta-code">{selectedJob.source_path}</span>
                </div>
                <div className="backup-meta-cell">
                  <span className="backup-meta-label">本机保存目录</span>
                  <span className="backup-meta-value backup-meta-code">{selectedJob.destination_dir}</span>
                </div>
                <div className="backup-meta-cell">
                  <span className="backup-meta-label">定时计划 / 下次执行</span>
                  <span className="backup-meta-value">
                    {formatScheduleSummary(selectedJob)}
                    {selectedJob.next_run_at ? ` · ${formatDateTime(selectedJob.next_run_at)}` : ''}
                  </span>
                </div>
                <div className="backup-meta-cell">
                  <span className="backup-meta-label">保留策略</span>
                  <span className="backup-meta-value">
                    {selectedJob.retention_count > 0 ? `最多保留最新 ${selectedJob.retention_count} 个成功版本` : '不限制版本数'}
                  </span>
                </div>
              </div>

              <div className="backups-pane-scroll">
                <Group justify="space-between" px={2}>
                  <Text fw={600} size="sm">版本历史 ({versions.length})</Text>
                </Group>

                {versions.length === 0 ? (
                  <Stack align="center" py="xl" gap="xs">
                    <Text size="sm" c="dimmed">该任务尚未生成任何备份版本</Text>
                    <Button
                      size="xs"
                      variant="light"
                      leftSection={<IconPlayerPlay size={14} />}
                      onClick={() => {
                        setRunNote('')
                        setRunModalJob(selectedJob)
                      }}
                    >
                      立即执行首次备份
                    </Button>
                  </Stack>
                ) : (
                  versions.map((v) => {
                    const vBadge = statusBadge(v.status)
                    const isEditingNote = editingVersionId === v.id
                    return (
                      <div key={v.id} className="backup-version-card">
                        <Group justify="space-between" gap="xs" wrap="wrap">
                          <Group gap={6} wrap="wrap">
                            <Badge size="sm" variant="filled">{v.version_label}</Badge>
                            <Badge size="xs" variant="light" color={v.trigger_kind === 'scheduled' ? 'grape' : 'blue'}>
                              {v.trigger_kind === 'scheduled' ? '定时' : '手动'}
                            </Badge>
                            <Badge size="xs" variant="dot" color={vBadge.color}>
                              {vBadge.label}
                            </Badge>
                            {v.source_kind !== 'unknown' && (
                              <Badge size="xs" variant="outline" color="gray">
                                {v.source_kind === 'directory' ? '目录' : '文件'} · {v.file_count} 个文件 · {formatBytes(v.total_bytes)}
                              </Badge>
                            )}
                            {v.status === 'success' && !v.exists_on_disk && (
                              <Badge size="xs" color="orange" variant="light">本机目录已不存在</Badge>
                            )}
                          </Group>
                          <Text size="xs" c="dimmed">{formatDateTime(v.created_at)}</Text>
                        </Group>

                        {v.status === 'failed' && v.error_detail && (
                          <Alert color="red" variant="light" p="xs" icon={<IconAlertCircle size={15} />}>
                            {v.error_detail}
                          </Alert>
                        )}

                        <div className="backup-note-box">
                          {isEditingNote ? (
                            <Group gap="xs" style={{ flex: 1 }}>
                              <TextInput
                                size="xs"
                                style={{ flex: 1 }}
                                aria-label="编辑版本备注"
                                value={editingNoteText}
                                onChange={(e) => setEditingNoteText(e.currentTarget.value)}
                                placeholder="输入版本备注…"
                              />
                              <ActionIcon
                                size="sm"
                                color="teal"
                                variant="light"
                                aria-label="保存备注"
                                loading={updateNoteMutation.isPending}
                                onClick={() =>
                                  updateNoteMutation.mutate({ versionId: v.id, note: editingNoteText })
                                }
                              >
                                <IconCheck size={14} />
                              </ActionIcon>
                              <ActionIcon
                                size="sm"
                                variant="subtle"
                                aria-label="取消编辑备注"
                                onClick={() => setEditingVersionId(null)}
                              >
                                <IconX size={14} />
                              </ActionIcon>
                            </Group>
                          ) : (
                            <>
                              <Text size="xs" style={{ flex: 1 }}>
                                备注：{v.note || <Text span c="dimmed">暂无备注</Text>}
                              </Text>
                              <Button
                                size="compact-xs"
                                variant="subtle"
                                leftSection={<IconEdit size={12} />}
                                onClick={() => {
                                  setEditingVersionId(v.id)
                                  setEditingNoteText(v.note || '')
                                }}
                              >
                                编辑备注
                              </Button>
                            </>
                          )}
                        </div>

                        <Group justify="space-between" align="center" gap="xs" wrap="wrap">
                          <Text className="backup-path-line" style={{ flex: 1 }}>
                            {v.local_path}
                          </Text>
                          <Group gap="xs">
                            <Button
                              size="compact-xs"
                              variant="light"
                              disabled={v.status !== 'success' || !v.exists_on_disk || selectedJob.is_running}
                              onClick={() => setRestoreVersion(v)}
                            >
                              还原
                            </Button>
                            <Button
                              size="compact-xs"
                              variant="light"
                              leftSection={<IconFolderOpen size={13} />}
                              disabled={v.status !== 'success' || !v.exists_on_disk}
                              onClick={() => openFolderMutation.mutate(v.id)}
                            >
                              打开目录
                            </Button>
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="red"
                              leftSection={<IconTrash size={13} />}
                              disabled={v.status === 'running'}
                              onClick={() => {
                                setDeleteFilesChecked(true)
                                setDeleteTarget({ kind: 'version', item: v })
                              }}
                            >
                              删除
                            </Button>
                          </Group>
                        </Group>
                      </div>
                    )
                  })
                )}
              </div>
            </>
          )}
        </section>
      </div>

      {restoreVersion && (
        <BackupRestoreModal
          version={restoreVersion}
          targets={targetsQuery.data?.items || []}
          onClose={() => setRestoreVersion(null)}
          onSettled={invalidateAll}
        />
      )}

      <BackupJobModal
        opened={jobForm !== null}
        onClose={() => setJobForm(null)}
        job={jobForm && jobForm.mode !== 'create' ? jobForm.job : null}
        cloning={jobForm?.mode === 'clone'}
        targets={targetsQuery.data?.items || []}
        defaultDestinationDir={targetsQuery.data?.default_destination_dir || ''}
        submitting={saveJobMutation.isPending}
        errorMessage={jobFormError}
        onSubmit={(payload, copyVersions) => saveJobMutation.mutate({ payload, copyVersions })}
      />

      <Modal
        opened={runModalJob !== null}
        onClose={() => setRunModalJob(null)}
        title={`手动备份 · ${runModalJob?.name || ''}`}
        centered
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            源路径：<code>{runModalJob?.source_path}</code>
          </Text>
          <Textarea
            label="本次版本备注（可选）"
            placeholder="例如：上线前备份、重构前留存…"
            autosize
            minRows={2}
            value={runNote}
            onChange={(e) => setRunNote(e.currentTarget.value)}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={() => setRunModalJob(null)}>取消</Button>
            <Button
              loading={runJobMutation.isPending}
              onClick={() => {
                if (runModalJob) {
                  runJobMutation.mutate({ jobId: runModalJob.id, note: runNote })
                }
              }}
            >
              开始备份
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        title={deleteTarget?.kind === 'job' ? '确认删除备份任务' : '确认删除备份版本'}
        centered
      >
        <Stack gap="sm">
          <Text size="sm">
            {deleteTarget?.kind === 'job'
              ? `确定删除备份任务「${deleteTarget.item.name}」及其全部版本记录吗？`
              : `确定删除版本「${deleteTarget?.item.version_label}」记录吗？`}
          </Text>
          <Checkbox
            label="同时删除本机磁盘上的对应备份文件"
            checked={deleteFilesChecked}
            onChange={(e) => setDeleteFilesChecked(e.currentTarget.checked)}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={() => setDeleteTarget(null)}>取消</Button>
            <Button color="red" loading={deleteMutation.isPending} onClick={() => deleteMutation.mutate()}>
              确认删除
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
