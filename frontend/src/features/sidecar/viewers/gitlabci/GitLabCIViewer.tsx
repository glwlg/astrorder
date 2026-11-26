import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  SegmentedControl,
  Stack,
  Text,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBrandGitlab,
  IconCopy,
  IconExternalLink,
  IconFileText,
  IconGitBranch,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
  IconRotateClockwise,
  IconSettings,
} from '@tabler/icons-react'
import {
  gitlabApi,
  type GitLabJobSummary,
  type GitLabJobTraceResponse,
  type GitLabPipelineDetailResponse,
  type GitLabPipelinesResponse,
} from '../../../../api/gitlab'
import {
  formatPipelineDuration,
  formatRelativeTime,
  gitLabStatusMeta,
  isActiveGitLabStatus,
} from '../../../../hooks/useGitLabCI'
import type { ViewerContext } from '../../types'
import './gitlabci.css'

function parseInitialPipelineId(readUrl?: string): number | null {
  if (!readUrl) return null
  try {
    const query = readUrl.startsWith('?') ? readUrl : new URL(readUrl, 'http://localhost').search
    const raw = new URLSearchParams(query).get('pipeline_id')
    if (!raw) return null
    const parsed = Number.parseInt(raw, 10)
    return Number.isFinite(parsed) && parsed > 0 ? parsed : null
  } catch {
    return null
  }
}

export function GitLabCIViewer({ artifact, session, isActive = true }: ViewerContext) {
  const workspace = artifact.path || session?.workspace || ''
  const connectionId = artifact.connectionId || session?.connection_id || undefined
  const sessionId = artifact.sessionId || session?.id || undefined
  const requestedPipelineId = useMemo(() => parseInitialPipelineId(artifact.readUrl), [artifact.readUrl])

  const [listData, setListData] = useState<GitLabPipelinesResponse | null>(null)
  const [listLoading, setListLoading] = useState(false)
  const [listError, setListError] = useState<string | null>(null)

  const [selectedPipelineId, setSelectedPipelineId] = useState<number | null>(requestedPipelineId)
  const [detailData, setDetailData] = useState<GitLabPipelineDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<string | null>(null)

  const [selectedJob, setSelectedJob] = useState<GitLabJobSummary | null>(null)
  const [tailLines, setTailLines] = useState<string>('200')
  const [traceData, setTraceData] = useState<GitLabJobTraceResponse | null>(null)
  const [traceLoading, setTraceLoading] = useState(false)
  const [traceError, setTraceError] = useState<string | null>(null)

  const [actionBusy, setActionBusy] = useState<string | null>(null)

  const targetParams = useMemo(
    () => ({
      workspace,
      connection_id: connectionId,
      session_id: sessionId,
    }),
    [workspace, connectionId, sessionId],
  )

  useEffect(() => {
    if (requestedPipelineId) {
      setSelectedPipelineId(requestedPipelineId)
    }
  }, [requestedPipelineId])

  const fetchList = useCallback(
    async (silent = false) => {
      if (!workspace) return
      if (!silent) setListLoading(true)
      setListError(null)
      try {
        const resp = await gitlabApi.listPipelines({ ...targetParams, limit: 15 })
        setListData(resp)
        setSelectedPipelineId((current) => {
          if (current && resp.pipelines.some((p) => p.id === current)) return current
          if (resp.latest_pipeline) return resp.latest_pipeline.id
          if (resp.pipelines.length > 0) return resp.pipelines[0].id
          return null
        })
      } catch (err) {
        if (!silent) {
          setListError(err instanceof Error ? err.message : '拉取 GitLab CI 流水线失败')
        }
      } finally {
        if (!silent) setListLoading(false)
      }
    },
    [targetParams, workspace],
  )

  const fetchDetail = useCallback(
    async (pipelineId: number, silent = false) => {
      if (!workspace || !pipelineId) return
      if (!silent) setDetailLoading(true)
      setDetailError(null)
      try {
        const resp = await gitlabApi.getPipelineDetail(pipelineId, targetParams)
        setDetailData(resp)
        setSelectedJob((prev) => {
          if (prev) {
            const refreshed = resp.jobs.find((j) => j.id === prev.id)
            if (refreshed) return refreshed
          }
          // Auto-select the first failed job so user immediately sees failure context
          const failedJob = resp.jobs.find((j) => j.status === 'failed' && !j.is_bridge)
          return failedJob || null
        })
      } catch (err) {
        if (!silent) {
          setDetailError(err instanceof Error ? err.message : '拉取流水线详情失败')
        }
      } finally {
        if (!silent) setDetailLoading(false)
      }
    },
    [targetParams, workspace],
  )

  const fetchTrace = useCallback(
    async (job: GitLabJobSummary, tail: number, silent = false) => {
      if (!workspace || !job.id || job.is_bridge) return
      if (!silent) setTraceLoading(true)
      setTraceError(null)
      try {
        const resp = await gitlabApi.getJobTrace(job.id, { ...targetParams, tail })
        setTraceData(resp)
      } catch (err) {
        if (!silent) {
          setTraceError(err instanceof Error ? err.message : '读取作业日志失败')
        }
      } finally {
        if (!silent) setTraceLoading(false)
      }
    },
    [targetParams, workspace],
  )

  useEffect(() => {
    void fetchList(false)
  }, [fetchList])

  useEffect(() => {
    if (!selectedPipelineId || !listData?.matched) {
      setDetailData(null)
      return
    }
    void fetchDetail(selectedPipelineId, false)
  }, [selectedPipelineId, listData?.matched, fetchDetail])

  useEffect(() => {
    if (!selectedJob || selectedJob.is_bridge) {
      setTraceData(null)
      setTraceError(null)
      return
    }
    void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, false)
  }, [selectedJob?.id, selectedJob?.is_bridge, tailLines, fetchTrace])

  // Auto-refresh while active tab is open
  useEffect(() => {
    if (!isActive || !listData?.matched) return
    const active =
      isActiveGitLabStatus(listData.latest_pipeline?.status) ||
      isActiveGitLabStatus(detailData?.pipeline?.status)
    const intervalMs = active ? 8_000 : 35_000
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return
      void fetchList(true)
      if (selectedPipelineId) {
        void fetchDetail(selectedPipelineId, true)
      }
      if (selectedJob && !selectedJob.is_bridge && isActiveGitLabStatus(selectedJob.status)) {
        void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, true)
      }
    }, intervalMs)
    return () => window.clearInterval(timer)
  }, [isActive, listData?.matched, listData?.latest_pipeline?.status, detailData?.pipeline?.status, selectedPipelineId, selectedJob, tailLines, fetchList, fetchDetail, fetchTrace])

  const openGitLabSettings = () => {
    window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'integrations' } }))
  }

  const handlePipelineAction = async (action: 'retry' | 'cancel') => {
    if (!selectedPipelineId) return
    setActionBusy(`pipeline:${action}`)
    try {
      if (action === 'retry') {
        await gitlabApi.retryPipeline(selectedPipelineId, targetParams)
        notifications.show({ color: 'blue', message: `已重新触发流水线 #${selectedPipelineId}` })
      } else {
        await gitlabApi.cancelPipeline(selectedPipelineId, targetParams)
        notifications.show({ color: 'gray', message: `已取消流水线 #${selectedPipelineId}` })
      }
      await fetchList(true)
      await fetchDetail(selectedPipelineId, true)
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '操作流水线失败' })
    } finally {
      setActionBusy(null)
    }
  }

  const handleJobAction = async (job: GitLabJobSummary, action: 'retry' | 'play') => {
    setActionBusy(`job:${job.id}:${action}`)
    try {
      const resp =
        action === 'retry'
          ? await gitlabApi.retryJob(job.id, targetParams)
          : await gitlabApi.playJob(job.id, targetParams)
      notifications.show({
        color: 'blue',
        message: action === 'retry' ? `已重试作业「${job.name}」` : `已触发手动作业「${job.name}」`,
      })
      if (resp.job) {
        setSelectedJob(resp.job)
      }
      if (selectedPipelineId) {
        await fetchDetail(selectedPipelineId, true)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '操作作业失败' })
    } finally {
      setActionBusy(null)
    }
  }

  const handleCopyTrace = async () => {
    if (!traceData?.trace) return
    try {
      await navigator.clipboard.writeText(traceData.trace)
      notifications.show({ color: 'blue', message: '已复制作业日志' })
    } catch {
      notifications.show({ color: 'orange', message: '复制失败，请手动选中复制' })
    }
  }

  const repo = listData?.repo
  const instance = listData?.instance
  const activePipeline = detailData?.pipeline || listData?.pipelines.find((p) => p.id === selectedPipelineId) || null

  return (
    <div className="gitlab-ci-viewer">
      <div className="gitlab-ci-header">
        <div className="gitlab-ci-header-meta">
          <IconBrandGitlab size={18} color="#e24329" style={{ flexShrink: 0 }} />
          <div style={{ minWidth: 0 }}>
            <div className="gitlab-ci-repo-title">
              {repo?.project_path || 'GitLab CI 流水线'}
            </div>
            <div className="gitlab-ci-repo-sub">
              {instance && <span>{instance.name}</span>}
              {repo?.branch && (
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
                  <IconGitBranch size={12} />
                  {repo.branch}
                </span>
              )}
              {repo?.head_sha && <span>HEAD {repo.head_sha.slice(0, 8)}</span>}
            </div>
          </div>
        </div>

        <Group gap={6} wrap="nowrap">
          {activePipeline?.web_url && (
            <Tooltip label="在 GitLab 网页中打开" withArrow>
              <ActionIcon
                component="a"
                href={activePipeline.web_url}
                target="_blank"
                rel="noopener noreferrer"
                size="sm"
                variant="subtle"
                color="gray"
                aria-label="在 GitLab 打开"
              >
                <IconExternalLink size={15} />
              </ActionIcon>
            </Tooltip>
          )}
          <Tooltip label="刷新流水线" withArrow>
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              aria-label="刷新 GitLab CI 流水线"
              loading={listLoading}
              onClick={() => {
                void fetchList(false)
                if (selectedPipelineId) void fetchDetail(selectedPipelineId, false)
              }}
            >
              <IconRefresh size={15} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="管理 GitLab 实例配置" withArrow>
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              aria-label="GitLab 设置"
              onClick={openGitLabSettings}
            >
              <IconSettings size={15} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </div>

      {!workspace ? (
        <div className="gitlab-ci-empty">
          <div className="gitlab-ci-empty-card">
            <Stack align="center" gap="xs">
              <IconBrandGitlab size={30} color="var(--astr-muted)" />
              <Text fw={600} size="sm">当前会话未绑定工作区目录</Text>
              <Text size="xs" c="dimmed">请在带有 Git 仓库的工作区会话中查看 GitLab CI 流水线。</Text>
            </Stack>
          </div>
        </div>
      ) : listError ? (
        <div className="gitlab-ci-empty">
          <div className="gitlab-ci-empty-card">
            <Stack align="center" gap="xs">
              <IconBrandGitlab size={30} color="#e03131" />
              <Text fw={600} size="sm">无法连接 GitLab CI</Text>
              <Text size="xs" c="red">{listError}</Text>
              <Group gap="xs" mt="xs">
                <Button size="xs" variant="default" onClick={() => { void fetchList(false) }}>
                  重试
                </Button>
                <Button size="xs" color="dark" onClick={openGitLabSettings}>
                  检查 GitLab 配置
                </Button>
              </Group>
            </Stack>
          </div>
        </div>
      ) : listLoading && !listData ? (
        <div className="gitlab-ci-empty">
          <Stack align="center" gap="xs">
            <Loader size="sm" color="gray" />
            <Text size="xs" c="dimmed">正在检测 GitLab 仓库并加载流水线…</Text>
          </Stack>
        </div>
      ) : listData && !listData.matched ? (
        <div className="gitlab-ci-empty">
          <div className="gitlab-ci-empty-card">
            <Stack align="center" gap="sm">
              <IconBrandGitlab size={32} color="#e24329" />
              <Text fw={600} size="sm">
                {listData.configured ? '当前仓库尚未匹配到 GitLab 实例' : '尚未配置 GitLab 实例'}
              </Text>
              <Text size="xs" c="dimmed">
                {listData.reason || '请在设置中添加您的自建 GitLab 地址与 Personal Access Token。'}
              </Text>
              {repo?.remote_url && (
                <Text size="xs" c="dimmed" ff="monospace">
                  检测到 Remote：{repo.remote_url}
                </Text>
              )}
              <Text size="xs" c="dimmed">
                配置完成后，星序会自动按主机名匹配仓库，Agent 也可直接使用 <Code>astrorder-agent gitlab status</Code> 查询 CI。
              </Text>
              <Group gap="xs" mt={4}>
                <Button size="xs" color="dark" leftSection={<IconSettings size={14} />} onClick={openGitLabSettings}>
                  前往配置 GitLab 实例
                </Button>
                <Button size="xs" variant="default" onClick={() => { void fetchList(false) }}>
                  重新检测
                </Button>
              </Group>
            </Stack>
          </div>
        </div>
      ) : listData && listData.pipelines.length === 0 ? (
        <div className="gitlab-ci-empty">
          <div className="gitlab-ci-empty-card">
            <Stack align="center" gap="xs">
              <IconBrandGitlab size={30} color="var(--astr-muted)" />
              <Text fw={600} size="sm">暂无 CI 流水线记录</Text>
              <Text size="xs" c="dimmed">
                仓库 <Code>{repo?.project_path}</Code> 还没有运行过 GitLab CI 流水线，提交并推送代码后点击刷新即可查看。
              </Text>
              <Button size="xs" variant="default" mt="xs" onClick={() => { void fetchList(false) }}>
                刷新
              </Button>
            </Stack>
          </div>
        </div>
      ) : (
        <div className="gitlab-ci-body">
          {/* 左侧：近期流水线列表 */}
          <div className="gitlab-ci-list-pane" role="listbox" aria-label="GitLab CI 流水线列表">
            {(listData?.pipelines || []).map((pipeline) => {
              const meta = gitLabStatusMeta(pipeline.status)
              const selected = pipeline.id === selectedPipelineId
              return (
                <UnstyledButton
                  key={pipeline.id}
                  role="option"
                  aria-selected={selected}
                  className={`gitlab-ci-pipeline-row${selected ? ' is-selected' : ''}`}
                  onClick={() => {
                    setSelectedPipelineId(pipeline.id)
                    setSelectedJob(null)
                    setTraceData(null)
                  }}
                >
                  <div className="gitlab-ci-pipeline-top">
                    <span className="gitlab-ci-pipeline-id">
                      <span
                        className={`gitlab-ci-status-dot${meta.spinning ? ' is-spinning' : ''}`}
                        style={{ backgroundColor: meta.dotColor }}
                      />
                      #{pipeline.id}
                    </span>
                    <Group gap={4} wrap="nowrap">
                      {pipeline.matches_head && (
                        <Badge size="xs" variant="light" color="indigo" px={5}>
                          HEAD
                        </Badge>
                      )}
                      <Text size="xs" fw={500} style={{ color: meta.dotColor }}>
                        {meta.label}
                      </Text>
                    </Group>
                  </div>
                  <div className="gitlab-ci-pipeline-meta">
                    <span className="gitlab-ci-pipeline-ref" title={pipeline.ref}>
                      {pipeline.ref}
                    </span>
                    <span>{pipeline.short_sha}</span>
                  </div>
                  <div className="gitlab-ci-pipeline-meta">
                    <span>{formatRelativeTime(pipeline.created_at || pipeline.updated_at)}</span>
                    <span>{formatPipelineDuration(pipeline.duration)}</span>
                  </div>
                </UnstyledButton>
              )
            })}
          </div>

          {/* 右侧：流水线详情、阶段作业列表与内联日志 */}
          <div className="gitlab-ci-detail-pane">
            {detailLoading && !detailData ? (
              <Stack align="center" justify="center" py="xl" gap="xs">
                <Loader size="sm" color="gray" />
                <Text size="xs" c="dimmed">正在加载阶段与作业详情…</Text>
              </Stack>
            ) : detailError ? (
              <Stack align="center" justify="center" py="xl" gap="xs">
                <Text size="xs" c="red">{detailError}</Text>
                {selectedPipelineId && (
                  <Button size="xs" variant="default" onClick={() => { void fetchDetail(selectedPipelineId, false) }}>
                    重试
                  </Button>
                )}
              </Stack>
            ) : activePipeline ? (
              <>
                <div className="gitlab-ci-summary-card">
                  <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <Group gap="xs" wrap="wrap">
                        {(() => {
                          const meta = gitLabStatusMeta(activePipeline.status)
                          return (
                            <Group gap={6} wrap="nowrap">
                              <span
                                className={`gitlab-ci-status-dot${meta.spinning ? ' is-spinning' : ''}`}
                                style={{ backgroundColor: meta.dotColor }}
                              />
                              <Text fw={600} size="sm" ff="monospace">
                                Pipeline #{activePipeline.id}
                              </Text>
                              <Badge size="xs" variant="light" color="gray">
                                {meta.label}
                              </Badge>
                            </Group>
                          )
                        })()}
                        {activePipeline.matches_head && (
                          <Badge size="xs" variant="light" color="indigo">当前 HEAD</Badge>
                        )}
                        {activePipeline.source && (
                          <Badge size="xs" variant="outline" color="gray">{activePipeline.source}</Badge>
                        )}
                      </Group>
                      <Text size="xs" c="dimmed" mt={4} ff="monospace">
                        {activePipeline.ref} · {activePipeline.short_sha}
                        {activePipeline.duration ? ` · 耗时 ${formatPipelineDuration(activePipeline.duration)}` : ''}
                        {activePipeline.created_at ? ` · ${formatRelativeTime(activePipeline.created_at)}` : ''}
                      </Text>
                    </div>

                    <Group gap="xs" wrap="nowrap">
                      {isActiveGitLabStatus(activePipeline.status) ? (
                        <Button
                          size="xs"
                          variant="default"
                          color="red"
                          leftSection={<IconPlayerStop size={13} />}
                          loading={actionBusy === 'pipeline:cancel'}
                          onClick={() => { void handlePipelineAction('cancel') }}
                        >
                          取消
                        </Button>
                      ) : (
                        <Button
                          size="xs"
                          variant="default"
                          leftSection={<IconRotateClockwise size={13} />}
                          loading={actionBusy === 'pipeline:retry'}
                          onClick={() => { void handlePipelineAction('retry') }}
                        >
                          重试流水线
                        </Button>
                      )}
                    </Group>
                  </Group>
                </div>

                {/* 阶段与作业 */}
                <div className="gitlab-ci-stages-grid">
                  {(detailData?.stages || []).map((stage) => {
                    const stageMeta = gitLabStatusMeta(stage.status)
                    return (
                      <div key={stage.name} className="gitlab-ci-stage-card">
                        <div className="gitlab-ci-stage-header">
                          <Group gap={6}>
                            <span
                              className={`gitlab-ci-status-dot${stageMeta.spinning ? ' is-spinning' : ''}`}
                              style={{ backgroundColor: stageMeta.dotColor }}
                            />
                            <span>{stage.name}</span>
                          </Group>
                          <Text size="xs" c="dimmed" fw={400}>
                            {stage.jobs.length} 个作业 · {stageMeta.label}
                          </Text>
                        </div>

                        <div className="gitlab-ci-jobs-list">
                          {stage.jobs.map((job) => {
                            const jobMeta = gitLabStatusMeta(job.status)
                            const isSelected = selectedJob?.id === job.id
                            return (
                              <div
                                key={job.id}
                                className={`gitlab-ci-job-row${isSelected ? ' is-selected' : ''}`}
                              >
                                <div
                                  className="gitlab-ci-job-main"
                                  role="button"
                                  tabIndex={0}
                                  onClick={() => setSelectedJob(job)}
                                  onKeyDown={(e) => {
                                    if (e.key === 'Enter' || e.key === ' ') {
                                      e.preventDefault()
                                      setSelectedJob(job)
                                    }
                                  }}
                                >
                                  <span
                                    className={`gitlab-ci-status-dot${jobMeta.spinning ? ' is-spinning' : ''}`}
                                    style={{ backgroundColor: jobMeta.dotColor }}
                                  />
                                  <div style={{ minWidth: 0, flex: 1 }}>
                                    <Group gap={6} wrap="nowrap">
                                      <Text size="xs" fw={600} truncate>{job.name}</Text>
                                      {job.is_bridge && (
                                        <Badge size="xs" variant="outline" color="gray">下游触发</Badge>
                                      )}
                                      {job.allow_failure && job.status === 'failed' && (
                                        <Badge size="xs" variant="light" color="orange">允许失败</Badge>
                                      )}
                                    </Group>
                                    {(job.failure_reason || job.runner_name) && (
                                      <Text size="xs" c="dimmed" truncate>
                                        {job.failure_reason ? `失败原因: ${job.failure_reason}` : ''}
                                        {job.failure_reason && job.runner_name ? ' · ' : ''}
                                        {job.runner_name ? `Runner: ${job.runner_name}` : ''}
                                      </Text>
                                    )}
                                  </div>
                                </div>

                                <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
                                  <Text size="xs" c="dimmed" ff="monospace">
                                    {formatPipelineDuration(job.duration)}
                                  </Text>
                                  <Text size="xs" fw={500} style={{ color: jobMeta.dotColor, minWidth: 42, textAlign: 'right' }}>
                                    {jobMeta.label}
                                  </Text>
                                  {!job.is_bridge && (
                                    <Tooltip label="查看作业日志" withArrow>
                                      <ActionIcon
                                        size="xs"
                                        variant={isSelected ? 'light' : 'subtle'}
                                        color="gray"
                                        aria-label={`查看 ${job.name} 日志`}
                                        onClick={() => setSelectedJob(job)}
                                      >
                                        <IconFileText size={13} />
                                      </ActionIcon>
                                    </Tooltip>
                                  )}
                                  {!job.is_bridge && job.status === 'manual' && (
                                    <Tooltip label="手动触发此作业" withArrow>
                                      <ActionIcon
                                        size="xs"
                                        variant="subtle"
                                        color="indigo"
                                        aria-label={`触发 ${job.name}`}
                                        loading={actionBusy === `job:${job.id}:play`}
                                        onClick={() => { void handleJobAction(job, 'play') }}
                                      >
                                        <IconPlayerPlay size={13} />
                                      </ActionIcon>
                                    </Tooltip>
                                  )}
                                  {!job.is_bridge && (job.status === 'failed' || job.status === 'canceled' || job.status === 'success') && (
                                    <Tooltip label="重试此作业" withArrow>
                                      <ActionIcon
                                        size="xs"
                                        variant="subtle"
                                        color="gray"
                                        aria-label={`重试 ${job.name}`}
                                        loading={actionBusy === `job:${job.id}:retry`}
                                        onClick={() => { void handleJobAction(job, 'retry') }}
                                      >
                                        <IconRotateClockwise size={13} />
                                      </ActionIcon>
                                    </Tooltip>
                                  )}
                                  {job.web_url && (
                                    <Tooltip label="在 GitLab 打开作业" withArrow>
                                      <ActionIcon
                                        component="a"
                                        href={job.web_url}
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        size="xs"
                                        variant="subtle"
                                        color="gray"
                                        aria-label={`在 GitLab 打开 ${job.name}`}
                                      >
                                        <IconExternalLink size={13} />
                                      </ActionIcon>
                                    </Tooltip>
                                  )}
                                </Group>
                              </div>
                            )
                          })}
                        </div>
                      </div>
                    )
                  })}
                </div>

                {/* 内联作业日志查看器 */}
                {selectedJob && !selectedJob.is_bridge && (
                  <div className="gitlab-ci-log-card">
                    <div className="gitlab-ci-log-header">
                      <Group gap={6} wrap="nowrap">
                        <IconFileText size={14} />
                        <Text size="xs" fw={600}>
                          作业日志 · {selectedJob.name} (#{selectedJob.id})
                        </Text>
                        {traceData && (
                          <Text size="xs" c="dimmed">
                            {traceData.truncated
                              ? `末 ${traceData.tail_lines} / 共 ${traceData.total_lines} 行`
                              : `共 ${traceData.total_lines} 行`}
                          </Text>
                        )}
                      </Group>

                      <Group gap={6} wrap="nowrap">
                        <SegmentedControl
                          size="xs"
                          value={tailLines}
                          onChange={setTailLines}
                          data={[
                            { label: '100行', value: '100' },
                            { label: '200行', value: '200' },
                            { label: '500行', value: '500' },
                          ]}
                        />
                        <Tooltip label="刷新日志" withArrow>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            loading={traceLoading}
                            aria-label="刷新作业日志"
                            onClick={() => {
                              void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, false)
                            }}
                          >
                            <IconRefresh size={13} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="复制日志" withArrow>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            aria-label="复制作业日志"
                            disabled={!traceData?.trace}
                            onClick={() => { void handleCopyTrace() }}
                          >
                            <IconCopy size={13} />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </div>

                    {traceLoading && !traceData ? (
                      <Stack align="center" py="md" gap={6}>
                        <Loader size="xs" color="gray" />
                        <Text size="xs" c="dimmed">正在读取作业输出…</Text>
                      </Stack>
                    ) : traceError ? (
                      <Text size="xs" c="red" p="sm">{traceError}</Text>
                    ) : (
                      <pre className="gitlab-ci-log-pre">
                        {traceData?.trace?.trim() ? traceData.trace : '（暂无日志输出）'}
                      </pre>
                    )}
                  </div>
                )}
              </>
            ) : null}
          </div>
        </div>
      )}
    </div>
  )
}
