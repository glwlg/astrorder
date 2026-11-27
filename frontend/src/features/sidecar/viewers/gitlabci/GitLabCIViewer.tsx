import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
  IconMaximize,
  IconMessageForward,
  IconMinimize,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
  IconRotateClockwise,
  IconSettings,
  IconX,
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
  const selectedJobIdRef = useRef<number | null>(null)
  selectedJobIdRef.current = selectedJob?.id ?? null
  const [logOpen, setLogOpen] = useState(false)
  const [logExpanded, setLogExpanded] = useState(false)
  const [tailLines, setTailLines] = useState<string>('200')
  const [traceData, setTraceData] = useState<GitLabJobTraceResponse | null>(null)
  const [traceLoading, setTraceLoading] = useState(false)
  const [traceError, setTraceError] = useState<string | null>(null)
  const traceReqSeqRef = useRef(0)
  const logBodyRef = useRef<HTMLDivElement | null>(null)
  const logPreRef = useRef<HTMLPreElement | null>(null)
  const [selectedLogText, setSelectedLogText] = useState('')
  const [selectionPos, setSelectionPos] = useState<{ left: number | null; top: number } | null>(null)

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
        const prevId = selectedJobIdRef.current
        const refreshed = prevId ? resp.jobs.find((j) => j.id === prevId) || null : null
        const failedJob = resp.jobs.find((j) => j.status === 'failed' && !j.is_bridge) || null
        const nextJob = refreshed || failedJob
        setSelectedJob(nextJob)
        if (!refreshed && failedJob && !silent) {
          setLogOpen(true)
        }
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
      const reqSeq = ++traceReqSeqRef.current
      if (!silent) {
        setTraceLoading(true)
        setTraceError(null)
      }
      try {
        const resp = await gitlabApi.getJobTrace(job.id, { ...targetParams, tail })
        if (reqSeq !== traceReqSeqRef.current) return
        setTraceData(resp)
        setTraceError(null)
        if (!silent) {
          window.requestAnimationFrame(() => {
            const pre = logPreRef.current
            if (pre) pre.scrollTop = pre.scrollHeight
          })
        }
      } catch (err) {
        if (reqSeq !== traceReqSeqRef.current) return
        if (!silent) {
          setTraceError(err instanceof Error ? err.message : '读取作业日志失败')
        }
      } finally {
        if (!silent && reqSeq === traceReqSeqRef.current) {
          setTraceLoading(false)
        }
      }
    },
    [targetParams, workspace],
  )

  const openJobLog = useCallback(
    (job: GitLabJobSummary) => {
      if (job.is_bridge) return
      setLogOpen(true)
      if (selectedJob?.id === job.id) {
        setSelectedJob(job)
        if (!traceLoading) {
          void fetchTrace(job, Number.parseInt(tailLines, 10) || 200, false)
        }
        return
      }
      setTraceData(null)
      setTraceError(null)
      setSelectedLogText('')
      setSelectionPos(null)
      setSelectedJob(job)
    },
    [fetchTrace, selectedJob?.id, tailLines, traceLoading],
  )

  const clearLogSelection = useCallback(() => {
    setSelectedLogText('')
    setSelectionPos(null)
    if (typeof window !== 'undefined') {
      const sel = window.getSelection()
      if (sel && !sel.isCollapsed && logPreRef.current) {
        const anchorInside = sel.anchorNode ? logPreRef.current.contains(sel.anchorNode) : false
        const focusInside = sel.focusNode ? logPreRef.current.contains(sel.focusNode) : false
        if (anchorInside || focusInside) {
          sel.removeAllRanges()
        }
      }
    }
  }, [])

  const captureLogSelection = useCallback(() => {
    const pre = logPreRef.current
    const body = logBodyRef.current
    if (!pre || !body || typeof window === 'undefined') {
      setSelectedLogText('')
      setSelectionPos(null)
      return
    }
    const sel = window.getSelection()
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      setSelectedLogText('')
      setSelectionPos(null)
      return
    }
    const range = sel.getRangeAt(0)
    const inLog =
      (sel.anchorNode ? pre.contains(sel.anchorNode) : false) ||
      (sel.focusNode ? pre.contains(sel.focusNode) : false) ||
      (typeof range.intersectsNode === 'function' ? range.intersectsNode(pre) : false)
    if (!inLog) {
      setSelectedLogText('')
      setSelectionPos(null)
      return
    }
    const snippet = sel.toString().trim()
    if (!snippet) {
      setSelectedLogText('')
      setSelectionPos(null)
      return
    }
    setSelectedLogText(snippet)
    const bodyRect = body.getBoundingClientRect()
    const rangeRect = typeof range.getBoundingClientRect === 'function' ? range.getBoundingClientRect() : null
    if (bodyRect.width > 0 && rangeRect && (rangeRect.width > 0 || rangeRect.height > 0)) {
      const centerX = rangeRect.left - bodyRect.left + rangeRect.width / 2
      const halfToolbar = 128
      const clampedLeft = Math.max(halfToolbar, Math.min(bodyRect.width - halfToolbar, centerX))
      const preferredTop = rangeRect.top - bodyRect.top - 42
      const top =
        preferredTop >= 8
          ? preferredTop
          : Math.max(8, Math.min(bodyRect.height - 44, rangeRect.bottom - bodyRect.top + 8))
      setSelectionPos({ left: clampedLeft, top })
    } else {
      setSelectionPos({ left: null, top: 10 })
    }
  }, [])

  useEffect(() => {
    if (!logOpen) {
      setSelectedLogText('')
      setSelectionPos(null)
      return
    }
    const onSelectionChange = () => {
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed) {
        setSelectedLogText('')
        setSelectionPos(null)
      }
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => document.removeEventListener('selectionchange', onSelectionChange)
  }, [logOpen])

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
    if (!logOpen || !selectedJob || selectedJob.is_bridge) {
      if (!selectedJob || selectedJob.is_bridge) {
        setTraceData(null)
        setTraceError(null)
      }
      return
    }
    void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, false)
  }, [logOpen, selectedJob?.id, selectedJob?.is_bridge, tailLines, fetchTrace])

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
      if (logOpen && selectedJob && !selectedJob.is_bridge && isActiveGitLabStatus(selectedJob.status)) {
        void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, true)
      }
    }, intervalMs)
    return () => window.clearInterval(timer)
  }, [isActive, listData?.matched, listData?.latest_pipeline?.status, detailData?.pipeline?.status, selectedPipelineId, logOpen, selectedJob, tailLines, fetchList, fetchDetail, fetchTrace])

  const openGitLabSettings = () => {
    window.dispatchEvent(
      new CustomEvent('astrorder:open-settings', {
        detail: { tab: 'plugins', pluginId: 'gitlab-ci-viewer' },
      }),
    )
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
        openJobLog(resp.job)
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

  const sendLogSnippetToComposer = useCallback(
    (snippet: string, isSelection = true) => {
      const cleaned = snippet.trim()
      if (!cleaned) return
      const maxLen = 4000
      const truncated = cleaned.length > maxLen
      const bodyText = truncated ? cleaned.slice(0, maxLen) : cleaned
      const repoLabel = repo?.project_path || workspace || 'GitLab CI'
      const pipelinePart = activePipeline
        ? `Pipeline #${activePipeline.id} (${activePipeline.ref} @ ${activePipeline.short_sha})`
        : ''
      const jobMeta = selectedJob ? gitLabStatusMeta(selectedJob.status) : null
      const jobPart = selectedJob
        ? `作业 \`${selectedJob.name}\` (#${selectedJob.id}${jobMeta ? `, ${jobMeta.label}` : ''})`
        : ''
      const header = ['[GitLab CI 日志片段]', repoLabel, pipelinePart, jobPart].filter(Boolean).join(' · ')
      const suffix =
        truncated && selectedJob
          ? `\n…（已截断，完整日志可通过 \`astrorder-agent ext call gitlab-ci-viewer log --param job_id=${selectedJob.id}\` 查看）`
          : ''
      const text = `${header}\n\`\`\`log\n${bodyText}${suffix}\n\`\`\`\n`
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: {
            agentId: session?.agent_id || artifact.agentId,
            sessionId: session?.id || artifact.sessionId,
            text,
          },
        }),
      )
      const lineCount = cleaned.split(/\r?\n/).length
      if (isSelection) {
        clearLogSelection()
      }
      notifications.show({
        color: 'indigo',
        message: isSelection
          ? `已将选中日志片段（${lineCount} 行）发送到对话框`
          : `已将作业「${selectedJob?.name || 'CI'}」日志发送到对话框`,
      })
    },
    [activePipeline, artifact.agentId, artifact.sessionId, clearLogSelection, repo?.project_path, selectedJob, session?.agent_id, session?.id, workspace],
  )

  const handleCopySnippet = useCallback(async () => {
    if (!selectedLogText) return
    try {
      await navigator.clipboard.writeText(selectedLogText)
      notifications.show({ color: 'blue', message: '已复制选中日志片段' })
    } catch {
      notifications.show({ color: 'orange', message: '复制失败，请手动复制' })
    }
  }, [selectedLogText])

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
                配置完成后，星序会自动按主机名匹配仓库，Agent 也可直接使用 <Code>astrorder-agent ext call gitlab-ci-viewer pipeline</Code> 查询 CI。
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
                    setLogOpen(false)
                    setLogExpanded(false)
                    setTraceData(null)
                    setTraceError(null)
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

          {/* 右侧：流水线详情、阶段作业列表与底部停靠日志面板 */}
          <div className="gitlab-ci-detail-pane">
            {!logExpanded && (
              <div className="gitlab-ci-detail-scroll">
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
                                const isSelected = logOpen && selectedJob?.id === job.id
                                return (
                                  <div
                                    key={job.id}
                                    className={`gitlab-ci-job-row${isSelected ? ' is-selected' : ''}`}
                                  >
                                    <div
                                      className="gitlab-ci-job-main"
                                      role="button"
                                      tabIndex={0}
                                      onClick={() => openJobLog(job)}
                                      onKeyDown={(e) => {
                                        if (e.key === 'Enter' || e.key === ' ') {
                                          e.preventDefault()
                                          openJobLog(job)
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
                                            variant={isSelected ? 'filled' : 'subtle'}
                                            color={isSelected ? 'indigo' : 'gray'}
                                            aria-label={`查看 ${job.name} 日志`}
                                            aria-pressed={isSelected}
                                            onClick={() => openJobLog(job)}
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
                  </>
                ) : null}
              </div>
            )}

            {/* 底部停靠作业日志面板（始终位于可视区域内，不会被长作业列表挤出视野） */}
            {activePipeline && logOpen && selectedJob && !selectedJob.is_bridge && (
              <div
                className={`gitlab-ci-log-dock${logExpanded ? ' is-expanded' : ''}`}
                role="region"
                aria-label={`作业日志 ${selectedJob.name}`}
              >
                <div className="gitlab-ci-log-header">
                  <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                    {(() => {
                      const jobMeta = gitLabStatusMeta(selectedJob.status)
                      return (
                        <span
                          className={`gitlab-ci-status-dot${jobMeta.spinning ? ' is-spinning' : ''}`}
                          style={{ backgroundColor: jobMeta.dotColor }}
                        />
                      )
                    })()}
                    <IconFileText size={14} style={{ flexShrink: 0 }} />
                    <Text size="xs" fw={600} truncate>
                      作业日志 · {selectedJob.name} (#{selectedJob.id})
                    </Text>
                    {traceData && (
                      <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
                        {traceData.truncated
                          ? `末 ${traceData.tail_lines} / 共 ${traceData.total_lines} 行`
                          : `共 ${traceData.total_lines} 行`}
                      </Text>
                    )}
                  </Group>

                  <Group gap={6} wrap="nowrap">
                    {selectedLogText ? (
                      <Button
                        size="compact-xs"
                        variant="filled"
                        color="indigo"
                        leftSection={<IconMessageForward size={13} />}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => sendLogSnippetToComposer(selectedLogText, true)}
                      >
                        发送选中到对话框 ({selectedLogText.split(/\r?\n/).length}行)
                      </Button>
                    ) : (
                      <Tooltip label="引用日志到对话框" withArrow>
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="indigo"
                          aria-label="引用日志到对话框"
                          disabled={!traceData?.trace?.trim()}
                          onClick={() => {
                            if (traceData?.trace?.trim()) {
                              sendLogSnippetToComposer(traceData.trace, false)
                            }
                          }}
                        >
                          <IconMessageForward size={13} />
                        </ActionIcon>
                      </Tooltip>
                    )}
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
                    <Tooltip label={logExpanded ? '还原分屏' : '最大化日志'} withArrow>
                      <ActionIcon
                        size="xs"
                        variant="subtle"
                        color="gray"
                        aria-label={logExpanded ? '还原分屏' : '最大化日志'}
                        onClick={() => setLogExpanded((v) => !v)}
                      >
                        {logExpanded ? <IconMinimize size={13} /> : <IconMaximize size={13} />}
                      </ActionIcon>
                    </Tooltip>
                    <Tooltip label="关闭日志面板" withArrow>
                      <ActionIcon
                        size="xs"
                        variant="subtle"
                        color="gray"
                        aria-label="关闭日志面板"
                        onClick={() => {
                          setLogOpen(false)
                          setLogExpanded(false)
                          clearLogSelection()
                        }}
                      >
                        <IconX size={13} />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                </div>

                <div ref={logBodyRef} className="gitlab-ci-log-body">
                  {selectedLogText && (
                    <div
                      role="toolbar"
                      aria-label="日志片段操作"
                      {...({ focusgroup: 'toolbar wrap' } as Record<string, string>)}
                      className={`gitlab-ci-selection-toolbar${selectionPos?.left == null ? ' is-corner' : ''}`}
                      style={
                        selectionPos
                          ? selectionPos.left != null
                            ? { left: `${selectionPos.left}px`, top: `${selectionPos.top}px` }
                            : { top: `${selectionPos.top}px` }
                          : undefined
                      }
                      onMouseDown={(e) => e.preventDefault()}
                    >
                      <button
                        type="button"
                        className="gitlab-ci-selection-btn is-primary"
                        onClick={() => sendLogSnippetToComposer(selectedLogText, true)}
                      >
                        <IconMessageForward size={13} aria-hidden="true" />
                        <span>发送到对话框</span>
                      </button>
                      <span role="separator" aria-orientation="vertical" className="gitlab-ci-selection-sep" />
                      <button
                        type="button"
                        className="gitlab-ci-selection-btn"
                        aria-label="复制选中片段"
                        title="复制选中片段"
                        onClick={() => { void handleCopySnippet() }}
                      >
                        <IconCopy size={13} aria-hidden="true" />
                        <span>复制</span>
                      </button>
                      <button
                        type="button"
                        className="gitlab-ci-selection-btn is-icon"
                        aria-label="取消选中"
                        title="取消选中"
                        onClick={clearLogSelection}
                      >
                        <IconX size={13} aria-hidden="true" />
                      </button>
                    </div>
                  )}
                  {traceLoading && !traceData ? (
                    <Stack align="center" justify="center" py="lg" gap={6} style={{ flex: 1 }}>
                      <Loader size="xs" color="gray" />
                      <Text size="xs" c="dimmed">正在读取「{selectedJob.name}」作业输出…</Text>
                    </Stack>
                  ) : traceError ? (
                    <Stack align="center" justify="center" p="md" gap={8} style={{ flex: 1 }}>
                      <Text size="xs" c="red">{traceError}</Text>
                      <Button
                        size="xs"
                        variant="default"
                        onClick={() => {
                          void fetchTrace(selectedJob, Number.parseInt(tailLines, 10) || 200, false)
                        }}
                      >
                        重试加载日志
                      </Button>
                    </Stack>
                  ) : (
                    <pre
                      ref={logPreRef}
                      className="gitlab-ci-log-pre"
                      onMouseUp={captureLogSelection}
                      onKeyUp={captureLogSelection}
                    >
                      {traceData?.trace?.trim() ? traceData.trace : '（暂无日志输出）'}
                    </pre>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
