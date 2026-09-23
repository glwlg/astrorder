import { useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { ActionIcon, Popover, Slider, Text, TextInput, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBolt,
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconRefresh,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { api, type OcxModelQuotaResponse } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSessionModel } from '../../hooks/useSessionModel'
import { REASONING_EFFORTS } from './composerMedia'
import './SessionModelControl.css'

type Choice = { provider: string; model: string; label: string }

const EFFORT_COLORS = ['#93c5fd', '#60a5fa', '#3b82f6', '#0066cc', '#1e40af']

const EFFORT_MARKS = [
  { value: 0, label: '' },
  { value: 1, label: '' },
  { value: 2, label: '' },
  { value: 3, label: '' },
  { value: 4, label: '' },
]

export function summarizeQuota(quota?: OcxModelQuotaResponse | null): { status: 'healthy' | 'tight' | 'exhausted'; label: string; detail: string } | null {
  if (!quota) return null
  if (quota.accounts && quota.accounts.length > 0) {
    const active = quota.accounts.filter((a) => !a.paused)
    if (active.length === 0) return { status: 'exhausted', label: '不可用', detail: '所有账号已暂停或暂无额度' }
    const usedList = active.map((a) => a.quota?.weeklyPercent ?? a.quota?.shortPercent ?? 0)
    const minUsed = Math.min(...usedList)
    const remaining = Math.max(0, 100 - minUsed)
    if (remaining <= 5) return { status: 'exhausted', label: '已耗尽', detail: `所有可用账号已用尽 (${minUsed}%)` }
    if (remaining <= 25) return { status: 'tight', label: `余${Math.round(remaining)}%`, detail: `最佳账号剩余 ${Math.round(remaining)}%` }
    return { status: 'healthy', label: `余${Math.round(remaining)}%`, detail: `账号配额充裕 (余 ${Math.round(remaining)}%)` }
  }
  if (quota.reports && quota.reports.length > 0) {
    const rep = quota.reports[0]
    if (!rep?.quota) return null
    const used = rep.quota.weeklyPercent ?? rep.quota.fiveHourPercent ?? rep.quota.monthlyPercent ?? 0
    const remaining = Math.max(0, 100 - used)
    if (remaining <= 5) return { status: 'exhausted', label: '已耗尽', detail: `${rep.label || rep.provider} 配额已耗尽 (${used}%)` }
    if (remaining <= 25) return { status: 'tight', label: `余${Math.round(remaining)}%`, detail: `${rep.label || rep.provider} 剩余偏紧 (余 ${Math.round(remaining)}%)` }
    return { status: 'healthy', label: `余${Math.round(remaining)}%`, detail: `${rep.label || rep.provider} 配额充裕 (余 ${Math.round(remaining)}%)` }
  }
  return null
}

export function ModelQuotaBadge({ modelRoute, size = 'sm' }: { modelRoute: string; size?: 'sm' | 'dot' }) {
  const query = useQuery({
    queryKey: ['astrorder', 'model-quota', modelRoute],
    queryFn: () => api.getModelQuota(modelRoute).catch(() => null),
    enabled: Boolean(modelRoute),
    retry: false,
    staleTime: 60000,
    refetchOnWindowFocus: false,
  })
  const summary = summarizeQuota(query.data)
  if (!summary) return null
  if (size === 'dot') {
    return <span className={`codex-model-quota-dot is-${summary.status}`} title={`${summary.label}: ${summary.detail}`} />
  }
  return (
    <span className={`codex-model-quota-badge is-${summary.status}`} title={summary.detail}>
      {summary.label}
    </span>
  )
}

export function SessionModelControl({ session }: { session: Session }) {
  const model = useSessionModel(session)
  const [opened, setOpened] = useState(false)
  const [view, setView] = useState<'main' | 'models'>('main')
  const [search, setSearch] = useState('')
  const [choice, setChoice] = useState<Choice | null>(null)
  const [changing, setChanging] = useState(false)
  const [draftEffortIndex, setDraftEffortIndex] = useState<number | null>(null)

  const options = useQuery({
    queryKey: ['astrorder', 'model-options', session.agent_id, session.id],
    queryFn: () => api.getSessionModels(session.id, session.agent_id),
    enabled: opened,
    staleTime: 30000,
    retry: false,
  })

  const applyChoice = async (target: Choice) => {
    setChoice(target)
    setChanging(true)
    try {
      await model.change(target.provider, target.model)
      setView('main')
    } catch (error) {
      notifications.show({
        message: error instanceof Error ? error.message : '模型切换未确认',
        color: 'red',
      })
    } finally {
      setChanging(false)
    }
  }

  const applyEffortByIndex = async (index: number) => {
    const effortObj = REASONING_EFFORTS[index]
    if (!effortObj || effortObj.value === model.effort) {
      setDraftEffortIndex(null)
      return
    }
    if (changing) return
    setChanging(true)
    try {
      await model.changeEffort(effortObj.value)
    } catch (error) {
      setDraftEffortIndex(null)
      notifications.show({
        message: error instanceof Error ? error.message : '思考强度未确认',
        color: 'red',
      })
    } finally {
      setChanging(false)
      setDraftEffortIndex(null)
    }
  }

  const resolvedEffortIndex = REASONING_EFFORTS.findIndex((item) => item.value === model.effort)
  const currentEffortIndex = resolvedEffortIndex >= 0
    ? resolvedEffortIndex
    : REASONING_EFFORTS.findIndex((item) => item.value === 'medium')
  const sliderIndex = draftEffortIndex ?? currentEffortIndex
  const effortLabel = REASONING_EFFORTS[sliderIndex]?.label || '中'
  const displayModelName = model.label
  const currentModelRoute = model.data?.model ? `${model.data.provider ? `${model.data.provider}/` : ''}${model.data.model}` : ''

  return (
    <Popover
      opened={opened}
      onChange={(open) => {
        setOpened(open)
        if (!open) setView('main')
      }}
      position="top-end"
      offset={10}
      shadow="md"
      radius={16}
      withArrow={false}
    >
      <Popover.Target>
        <button
          type="button"
          className="codex-model-pill"
          aria-label="选择会话模型"
          title={model.label}
          onClick={() => {
            setOpened((v) => !v)
            setView('main')
          }}
        > 
          <span className="codex-model-pill-text">{displayModelName}</span>
          <ModelQuotaBadge modelRoute={currentModelRoute} size="dot" />
          {effortLabel && <span className="codex-model-pill-effort">{effortLabel}</span>}
          <IconChevronDown size={14} className="codex-model-pill-arrow" />
        </button>
      </Popover.Target>

      <Popover.Dropdown className="codex-model-popover-dropdown">
        {view === 'main' ? (
          <div className="codex-model-card" style={{ '--effort-color': EFFORT_COLORS[sliderIndex] } as React.CSSProperties}>
            <div className="codex-model-header-row">
              <span className="codex-model-icon-bolt" title="思维与推理">
                <IconBolt size={18} />
              </span>
              <button
                type="button"
                className="codex-model-title-btn"
                aria-label="切换到选择模型"
                onClick={() => setView('models')}
              >
                <span className="codex-model-effort-row">
                  <AnimatePresence mode="popLayout" initial={false}>
                    <motion.span
                      key={effortLabel}
                      initial={{ opacity: 0, y: -3, scale: 0.95 }}
                      animate={{ opacity: 1, y: 0, scale: 1 }}
                      exit={{ opacity: 0, y: 3, scale: 0.95 }}
                      transition={{ duration: 0.16, ease: 'easeOut' }}
                      className="codex-model-effort-highlight"
                    >
                      {effortLabel}
                    </motion.span>
                  </AnimatePresence>
                  <IconChevronRight size={15} />
                </span>
                <span className="codex-model-name-label">{displayModelName}</span>
              </button>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="sm"
                aria-label="刷新模型"
                onClick={() => void options.refetch()}
                loading={options.isFetching}
              >
                <IconRefresh size={16} />
              </ActionIcon>
            </div>

            <div className="codex-model-slider-wrap">
              <Slider
                size={18}
                thumbSize={22}
                color={EFFORT_COLORS[sliderIndex]}
                min={0}
                max={4}
                step={1}
                marks={EFFORT_MARKS}
                value={sliderIndex}
                thumbLabel="思考强度"
                onChange={setDraftEffortIndex}
                onChangeEnd={(val) => void applyEffortByIndex(val)}
                label={(val) => REASONING_EFFORTS[val]?.label}
                styles={{
                  root: { width: '100%' },
                  track: {
                    borderRadius: 999,
                  },
                  bar: {
                    borderRadius: 999,
                    // Mantine extends both ends; intermediate fills must stop at the thumb.
                    width: sliderIndex === 4 ? undefined : `calc(${sliderIndex * 25}% + var(--slider-size))`,
                  },
                  thumb: {
                    border: '2px solid #ffffff',
                    backgroundColor: '#ffffff',
                    boxShadow: '0 1px 4px rgba(0,0,0,0.22)',
                  },
                  markWrapper: {
                    top: '50%',
                    insetInlineStart: 'var(--mark-offset)',
                  },
                  mark: {
                    width: 4,
                    height: 4,
                    border: 0,
                    borderRadius: '50%',
                    backgroundColor: 'rgba(0,0,0,0.25)',
                    transform: 'translate(-50%, -50%)',
                  },
                }}
              />
            </div>
          </div>
        ) : (
          <div className="codex-model-list-panel">
            <div className="codex-model-list-head">
              <Text size="xs" c="dimmed" fw={600}>
                选择模型
              </Text>
              <TextInput
                size="xs"
                placeholder="搜索模型…"
                value={search}
                onChange={(e) => setSearch(e.currentTarget.value)}
                autoFocus
                mt={4}
              />
            </div>
            <div className="codex-model-list-scroll">
              <div className="codex-model-list-section-title">默认 · 推荐模型集</div>
              {options.isFetching && (
                <Text size="xs" c="dimmed" p="xs">
                  读取可用模型…
                </Text>
              )}
              {options.data?.items
                .filter((item) =>
                  item.label.toLowerCase().includes(search.toLowerCase()),
                )
                .map((item) => {
                  const isSelected =
                    (choice?.provider === item.provider && choice?.model === item.model) ||
                    (!choice &&
                      model.data?.provider === item.provider &&
                      model.data?.model === item.model) ||
                    model.label.includes(item.model)

                  return (
                    <UnstyledButton
                      key={JSON.stringify([item.provider, item.model])}
                      className={"codex-model-menu-item" + (isSelected ? " is-selected" : "")}
                      onClick={() => void applyChoice(item)}
                      disabled={changing}
                    >
                      <span className="codex-model-menu-item-text">{item.label}</span>
                      <ModelQuotaBadge modelRoute={`${item.provider ? `${item.provider}/` : ''}${item.model}`} />
                      {isSelected && <IconCheck size={16} className="codex-model-check-icon" />}
                    </UnstyledButton>
                  )
                })}
            </div>
          </div>
        )}
      </Popover.Dropdown>
    </Popover>
  )
}
