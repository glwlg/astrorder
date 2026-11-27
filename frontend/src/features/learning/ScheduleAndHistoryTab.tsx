import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Button,
  Checkbox,
  Group,
  NumberInput,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import {
  IconArrowUpRight,
  IconCalendarClock,
  IconPlus,
  IconRobot,
  IconX,
} from '@tabler/icons-react'
import type { BotSummary } from '../../api/client'
import type {
  LearningScanRun,
  LearningScheduleConfig,
  LearningTopic,
} from '../../api/learning'
import { MasteryArchivePanel } from './MasteryArchivePanel'
import {
  WEEKDAY_LABELS,
  formatShortDateTime,
  splitSessionKey,
} from './useLearningRadar'

interface ScheduleAndHistoryTabProps {
  config: LearningScheduleConfig
  bots: BotSummary[]
  mentorBot?: BotSummary
  runs: LearningScanRun[]
  topics: LearningTopic[]
  savingConfig: boolean
  bootstrappingBot: boolean
  busyTopicId?: string | null
  onSaveConfig: (next: LearningScheduleConfig) => void
  onBootstrapMentorBot: () => void
  onRestoreTopic: (topic: LearningTopic) => void
  onExportProfile: () => void
  onJumpToRadar: () => void
}

function runStatusMeta(status: string): { label: string; dot: string } {
  switch (status) {
    case 'completed':
      return { label: '完成', dot: '#2f9e44' }
    case 'running':
      return { label: '运行中', dot: '#228be6' }
    case 'skipped':
      return { label: '已跳过', dot: '#868e96' }
    case 'timeout':
      return { label: '超时', dot: '#d97706' }
    case 'failed':
      return { label: '失败', dot: '#c92a2a' }
    default:
      return { label: status, dot: '#868e96' }
  }
}

export function ScheduleAndHistoryTab({
  config,
  bots,
  mentorBot,
  runs,
  topics,
  savingConfig,
  bootstrappingBot,
  busyTopicId,
  onSaveConfig,
  onBootstrapMentorBot,
  onRestoreTopic,
  onExportProfile,
  onJumpToRadar,
}: ScheduleAndHistoryTabProps) {
  const navigate = useNavigate()
  const [draft, setDraft] = useState<LearningScheduleConfig>(config)
  const [newExcluded, setNewExcluded] = useState('')

  const selectedBot = bots.find((b) => b.id === draft.mentor_bot_id || b.slug === draft.mentor_bot_id) || mentorBot

  const addExcluded = () => {
    const clean = newExcluded.trim()
    if (!clean) return
    if (!draft.excluded_workspaces.includes(clean)) {
      setDraft((prev) => ({
        ...prev,
        excluded_workspaces: [...prev.excluded_workspaces, clean],
      }))
    }
    setNewExcluded('')
  }

  const removeExcluded = (target: string) => {
    setDraft((prev) => ({
      ...prev,
      excluded_workspaces: prev.excluded_workspaces.filter((item) => item !== target),
    }))
  }

  const openSessionKey = (sessionKey?: string) => {
    const parsed = splitSessionKey(sessionKey)
    if (!parsed) return
    navigate(`/chat/${encodeURIComponent(parsed.sessionId)}?agent_id=${encodeURIComponent(parsed.agentId)}`)
  }

  return (
    <div className="learning-schedule-grid">
      {/* 左列：每周定时扫描计划 + 成长导师 Bot 绑定 */}
      <div className="learning-panel-card">
        <Group justify="space-between" align="center">
          <Group gap={8}>
            <IconCalendarClock size={18} />
            <div>
              <Text fw={600} size="sm">每周定时扫描计划</Text>
              <Text size="xs" c="dimmed">在非工作时段自动派发导师 Bot 静默整理盲区卡片</Text>
            </div>
          </Group>
          <Switch
            size="sm"
            label={draft.enabled ? '已启用' : '已暂停'}
            labelPosition="left"
            checked={draft.enabled}
            onChange={(e) => {
              const checked = e.currentTarget.checked
              setDraft((prev) => ({ ...prev, enabled: checked }))
            }}
          />
        </Group>

        <div>
          <Text size="xs" fw={500} mb={6}>执行星期选择</Text>
          <div className="learning-weekday-grid">
            {[1, 2, 3, 4, 5, 6, 0].map((wd) => (
              <button
                key={wd}
                type="button"
                className={`learning-weekday-btn${draft.weekday === wd ? ' is-active' : ''}`}
                onClick={() => setDraft((prev) => ({ ...prev, weekday: wd }))}
              >
                {WEEKDAY_LABELS[wd]}
              </button>
            ))}
          </div>
        </div>

        <Group grow align="flex-end" wrap="wrap">
          <TextInput
            size="xs"
            label="执行时间（本地时间）"
            placeholder="20:00"
            value={draft.time}
            onChange={(e) => {
              const value = e.currentTarget.value
              setDraft((prev) => ({ ...prev, time: value }))
            }}
          />
          <div>
            <Text size="xs" fw={500} mb={4}>溯源回溯窗口</Text>
            <SegmentedControl
              fullWidth
              size="xs"
              value={String(draft.lookback_days)}
              onChange={(val) => setDraft((prev) => ({ ...prev, lookback_days: Number(val) || 7 }))}
              data={[
                { label: '近 7 天', value: '7' },
                { label: '近 14 天', value: '14' },
                { label: '近 30 天', value: '30' },
              ]}
            />
          </div>
        </Group>

        <Stack gap={8}>
          <Checkbox
            size="xs"
            label="离线错过补跑策略（电脑休眠错过计划时，48 小时内开机自动补扫一次）"
            checked={draft.misfire_catchup_hours > 0}
            onChange={(e) => {
              const checked = e.currentTarget.checked
              setDraft((prev) => ({
                ...prev,
                misfire_catchup_hours: checked ? 48 : 0,
              }))
            }}
          />
          <Checkbox
            size="xs"
            label="灵动岛温和提醒（扫描产出新盲区卡片时在顶部胶囊提示，不弹打扰性弹窗）"
            checked={draft.notify_dynamic_island}
            onChange={(e) => {
              const checked = e.currentTarget.checked
              setDraft((prev) => ({ ...prev, notify_dynamic_island: checked }))
            }}
          />
        </Stack>

        {/* 4 路信号源 */}
        <div>
          <Text size="xs" fw={600} mb={8}>采集信号源（Source Traces）</Text>
          <div className="learning-signals-grid">
            <label className="learning-signal-tile">
              <Checkbox
                size="xs"
                mt={2}
                checked={draft.signal_sources.sessions}
                onChange={(e) => {
                  const checked = e.currentTarget.checked
                  setDraft((prev) => ({
                    ...prev,
                    signal_sources: { ...prev.signal_sources, sessions: checked },
                  }))
                }}
              />
              <div>
                <Text size="xs" fw={600}>跨 Agent 会话轨迹</Text>
                <Text size="xs" c="dimmed">Antigravity / Codex / Hermes 追问与调试线索</Text>
              </div>
            </label>

            <label className="learning-signal-tile">
              <Checkbox
                size="xs"
                mt={2}
                checked={draft.signal_sources.git}
                onChange={(e) => {
                  const checked = e.currentTarget.checked
                  setDraft((prev) => ({
                    ...prev,
                    signal_sources: { ...prev.signal_sources, git: checked },
                  }))
                }}
              />
              <div>
                <Text size="xs" fw={600}>Git 提交与变更快照</Text>
                <Text size="xs" c="dimmed">识别 AI 高频代写但未细读原理的黑盒模块</Text>
              </div>
            </label>

            <label className="learning-signal-tile">
              <Checkbox
                size="xs"
                mt={2}
                checked={draft.signal_sources.failures}
                onChange={(e) => {
                  const checked = e.currentTarget.checked
                  setDraft((prev) => ({
                    ...prev,
                    signal_sources: { ...prev.signal_sources, failures: checked },
                  }))
                }}
              />
              <div>
                <Text size="xs" fw={600}>构建与 CI/CD 失败日志</Text>
                <Text size="xs" c="dimmed">捕获任务报错与 GitLab CI 失败模式</Text>
              </div>
            </label>

            <label className="learning-signal-tile">
              <Checkbox
                size="xs"
                mt={2}
                checked={draft.signal_sources.meetings}
                onChange={(e) => {
                  const checked = e.currentTarget.checked
                  setDraft((prev) => ({
                    ...prev,
                    signal_sources: { ...prev.signal_sources, meetings: checked },
                  }))
                }}
              />
              <div>
                <Text size="xs" fw={600}>会议纪要与架构术语</Text>
                <Text size="xs" c="dimmed">提炼评审与讨论中出现的新领域概念</Text>
              </div>
            </label>
          </div>
        </div>

        {/* 驱动引擎：成长导师 Bot */}
        <div style={{ borderTop: '1px solid var(--astr-border)', paddingTop: 14 }}>
          <Group justify="space-between" align="center" mb={4}>
            <Group gap={6}>
              <IconRobot size={16} />
              <Text size="xs" fw={650}>驱动引擎 · 成长导师 Bot</Text>
            </Group>
            <Button
              size="compact-xs"
              variant="default"
              leftSection={<IconPlus size={12} />}
              loading={bootstrappingBot}
              onClick={onBootstrapMentorBot}
            >
              一键创建官方导师 Bot
            </Button>
          </Group>
          <Text size="xs" c="dimmed" mb={8}>
            所有每周盲区深度扫描、代码伴学讲解与笔记沉淀均由绑定的 Bot 依托成熟 Agent 平台执行，无需配置内置 LLM 接口。
          </Text>

          <Select
            size="xs"
            placeholder="选择负责扫描与教学的 Bot…"
            value={draft.mentor_bot_id || null}
            onChange={(val) => setDraft((prev) => ({ ...prev, mentor_bot_id: val || '' }))}
            data={bots.map((b) => ({
              value: b.id,
              label: `${b.name} (@${b.slug}) · ${b.workspace}`,
            }))}
            searchable
            clearable
          />

          {selectedBot && (
            <div className="learning-bot-preview" style={{ marginTop: 10 }}>
              <Group justify="space-between" align="center" wrap="wrap" gap="xs">
                <Group gap={6}>
                  <Text size="xs" fw={650}>{selectedBot.name}</Text>
                  <span className="learning-mono-chip">@{selectedBot.slug}</span>
                  <span className="learning-mono-chip">
                    {selectedBot.default_approval_mode === 'full_access' ? '完全访问' : selectedBot.default_approval_mode || '完全访问'}
                  </span>
                </Group>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="indigo"
                  rightSection={<IconArrowUpRight size={12} />}
                  onClick={() => navigate('/bots')}
                >
                  去 Bot 工作台编辑人设与技能
                </Button>
              </Group>
              <Text size="xs" c="dimmed" ff="monospace" truncate>
                Agent: {selectedBot.default_agent_id} · 工作区 {selectedBot.workspace}
              </Text>
              <Group gap={6}>
                {selectedBot.has_agents_md && <span className="learning-mono-chip">AGENTS.md</span>}
                {selectedBot.skills.map((sk) => (
                  <span key={sk.slug} className="learning-mono-chip">Skill: {sk.slug}</span>
                ))}
              </Group>
            </div>
          )}
        </div>

        {/* 过滤边界与降噪 */}
        <div style={{ borderTop: '1px solid var(--astr-border)', paddingTop: 14 }}>
          <Text size="xs" fw={600} mb={6}>扫描排除的工作区路径 / 关键词</Text>
          <Group gap={6} mb={8} wrap="wrap">
            {draft.excluded_workspaces.map((ex) => (
              <span key={ex} className="learning-mono-chip">
                {ex}
                <button
                  type="button"
                  aria-label={`移除排除项 ${ex}`}
                  style={{ border: 0, background: 'transparent', cursor: 'pointer', padding: 0, display: 'inline-flex', color: 'inherit' }}
                  onClick={() => removeExcluded(ex)}
                >
                  <IconX size={11} />
                </button>
              </span>
            ))}
          </Group>
          <Group gap={6}>
            <TextInput
              size="xs"
              style={{ flex: 1 }}
              placeholder="输入要排除的目录或项目关键词，如 scratchpad…"
              value={newExcluded}
              onChange={(e) => setNewExcluded(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  addExcluded()
                }
              }}
            />
            <Button size="xs" variant="default" onClick={addExcluded}>添加</Button>
          </Group>

          <Group gap="md" mt={10} wrap="wrap">
            <NumberInput
              size="xs"
              label="低频活跃降噪（最少活跃会话数）"
              min={0}
              max={50}
              value={draft.min_activity_sessions}
              onChange={(v) => setDraft((prev) => ({ ...prev, min_activity_sessions: Number(v) || 0 }))}
              w={210}
            />
            <NumberInput
              size="xs"
              label="或最少 Git 提交数"
              min={0}
              max={100}
              value={draft.min_activity_commits}
              onChange={(v) => setDraft((prev) => ({ ...prev, min_activity_commits: Number(v) || 0 }))}
              w={160}
            />
          </Group>
        </div>

        <Group justify="flex-end" gap="xs" pt={6} style={{ borderTop: '1px solid var(--astr-border)' }}>
          <Button
            size="xs"
            variant="subtle"
            color="gray"
            onClick={() => setDraft(config)}
          >
            重置修改
          </Button>
          <Button
            size="xs"
            color="dark"
            loading={savingConfig}
            onClick={() => onSaveConfig(draft)}
          >
            保存定时计划
          </Button>
        </Group>
      </div>

      {/* 右列：最近扫描记录 + 个人技能掌握档案 */}
      <Stack gap={16}>
        <div className="learning-panel-card">
          <Group justify="space-between" align="center">
            <Text fw={600} size="sm">最近扫描记录</Text>
            <Text size="xs" c="dimmed">共 {runs.length} 次</Text>
          </Group>

          {runs.length === 0 ? (
            <Text size="xs" c="dimmed" ta="center" py="md">
              尚未执行过盲区扫描，绑定导师 Bot 后点击右上角「立即扫描」即可体验。
            </Text>
          ) : (
            <Stack gap={8}>
              {runs.slice(0, 8).map((run) => {
                const st = runStatusMeta(run.status)
                return (
                  <div key={run.id} className="learning-run-row">
                    <Group justify="space-between" gap="xs" wrap="wrap">
                      <Group gap={6}>
                        <Text size="xs" fw={600} ff="monospace">{formatShortDateTime(run.started_at)}</Text>
                        <span className="learning-mono-chip">
                          {run.trigger === 'scheduled' ? '每周定时' : run.trigger === 'agent' ? 'Agent 触发' : '手动触发'}
                        </span>
                        <Text size="xs" c="dimmed">近 {run.lookback_days} 天</Text>
                      </Group>
                      <Group gap={6}>
                        {run.duration_ms ? (
                          <Text size="xs" c="dimmed" ff="monospace">{(run.duration_ms / 1000).toFixed(1)}s</Text>
                        ) : null}
                        <span className="learning-mono-chip">
                          <span
                            className={`learning-dot${run.status === 'running' ? ' is-pulsing' : ''}`}
                            style={{ backgroundColor: st.dot }}
                          />
                          {st.label}
                        </span>
                      </Group>
                    </Group>

                    <Group justify="space-between" align="center" gap="xs" wrap="wrap">
                      <Text size="xs" c="dimmed" style={{ flex: 1 }}>
                        {run.status === 'skipped' || run.status === 'failed' || run.status === 'timeout'
                          ? run.error || run.summary
                          : `采集 ${run.evidence_count} 条线索 → 新发现 ${run.new_topics_count} 个盲区 / 合并 ${run.merged_topics_count} 个`}
                      </Text>
                      <Group gap={4}>
                        {run.session_key && (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color="indigo"
                            rightSection={<IconArrowUpRight size={12} />}
                            onClick={() => openSessionKey(run.session_key)}
                          >
                            查看 Bot 会话
                          </Button>
                        )}
                        {run.status === 'completed' && (
                          <Button size="compact-xs" variant="subtle" color="gray" onClick={onJumpToRadar}>
                            查看卡片
                          </Button>
                        )}
                      </Group>
                    </Group>
                  </div>
                )
              })}
            </Stack>
          )}
        </div>

        <MasteryArchivePanel
          topics={topics}
          compact
          busyTopicId={busyTopicId}
          onRestoreTopic={onRestoreTopic}
          onExport={onExportProfile}
        />
      </Stack>
    </div>
  )
}
