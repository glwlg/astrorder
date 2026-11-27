import { useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { ActionIcon, Popover, Slider, Text, TextInput, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
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
import { isDirectAntigravityModelRoute } from '../agentAccounts/agyAccountsApi'
import { useIsOfficeExperience } from '../office/appProfile'
import {
  availableEffortsForModel,
  effortLabelOf,
  findModelGroup,
  groupModelChoices,
  isAntigravityProvider,
  normalizeEffortValue,
  parseAntigravityModelId,
  resolveGroupVariant,
  type ModelChoice,
  type ModelChoiceGroup,
} from './antigravityModels'
import './SessionModelControl.css'

const EFFORT_COLOR_BY_VALUE: Record<string, string> = {
  low: '#93c5fd',
  medium: '#60a5fa',
  high: '#3b82f6',
  xhigh: '#0066cc',
  max: '#1e40af',
}

export function formatDisplayModel(label: string): string {
  if (!label) return ''
  const [modelPart, ...rest] = label.split(' · ')
  const suffix = rest.length ? ' · ' + rest.join(' · ') : ''
  const slashIdx = modelPart.indexOf('/')
  const trimmed = slashIdx >= 0 ? modelPart.slice(slashIdx + 1) : modelPart
  return trimmed + suffix
}

export function summarizeQuota(quota?: OcxModelQuotaResponse | null): { status: 'healthy' | 'tight' | 'exhausted'; label: string; detail: string } | null {
  if (!quota) return null
  if (quota.accounts && quota.accounts.length > 0) {
    const active = quota.accounts.filter((a) => !a.paused)
    if (active.length === 0) return { status: 'exhausted', label: '不可用', detail: '所有账号已暂停或暂无额度' }
    const known = active.filter((a) => a.quota && !a.error)
    const usedList = known.map((a) => Math.max(...[a.quota?.weeklyPercent, a.quota?.shortPercent, ...(a.quota?.customWindows?.map((w) => w.percent) || [])].filter((v): v is number => v != null && Number.isFinite(v))))
      .filter(Number.isFinite)
    if (usedList.length === 0) return null
    const minUsed = Math.min(...usedList)
    const remaining = Math.max(0, 100 - minUsed)
    if (remaining <= 5) return { status: 'exhausted', label: '已耗尽', detail: `所有可用账号已用尽 (${minUsed}%)` }
    if (remaining <= 25) return { status: 'tight', label: `余${Math.round(remaining)}%`, detail: `最佳账号剩余 ${Math.round(remaining)}%` }
    return { status: 'healthy', label: `余${Math.round(remaining)}%`, detail: `账号配额充裕 (余 ${Math.round(remaining)}%)` }
  }
  if (quota.reports && quota.reports.length > 0) {
    const rep = quota.reports[0]
    if (!rep?.quota) return null
    const candidates = [
      rep.quota.weeklyPercent,
      rep.quota.fiveHourPercent,
      rep.quota.monthlyPercent,
      ...(rep.quota.customWindows?.map((w) => w.percent) || []),
    ].filter((v): v is number => v != null && Number.isFinite(v))
    if (candidates.length === 0) return null
    const used = Math.max(...candidates)
    const remaining = Math.max(0, 100 - used)
    if (remaining <= 5) return { status: 'exhausted', label: '已耗尽', detail: `${rep.label || rep.provider} 配额已耗尽 (${used}%)` }
    if (remaining <= 25) return { status: 'tight', label: `余${Math.round(remaining)}%`, detail: `${rep.label || rep.provider} 剩余偏紧 (余 ${Math.round(remaining)}%)` }
    return { status: 'healthy', label: `余${Math.round(remaining)}%`, detail: `${rep.label || rep.provider} 配额充裕 (余 ${Math.round(remaining)}%)` }
  }
  return null
}

export function ModelQuotaBadge({
  modelRoute,
  agentId,
  agentKind,
  size = 'sm',
}: {
  modelRoute: string
  agentId?: string
  agentKind?: string
  size?: 'sm' | 'dot'
}) {
  const paused = isDirectAntigravityModelRoute(modelRoute, agentKind)
  const query = useQuery({
    queryKey: ['astrorder', 'model-quota', modelRoute, agentId || ''],
    queryFn: () => api.getModelQuota(modelRoute, agentId).catch(() => null),
    enabled: Boolean(modelRoute) && !paused,
    retry: false,
    staleTime: 60000,
    refetchOnWindowFocus: false,
  })
  if (paused || query.data?.paused) return null
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

export function SessionModelControl({
  session,
  enabled = true,
  refetchOnMount = 'always',
  showPillQuota = true,
}: {
  session: Session
  enabled?: boolean
  refetchOnMount?: boolean | 'always'
  showPillQuota?: boolean
}) {
  const isOfficeMode = useIsOfficeExperience(session)
  const [opened, setOpened] = useState(false)
  const model = useSessionModel(session, {
    enabled: enabled || opened,
    refetchOnMount,
  })
  const [view, setView] = useState<'main' | 'models'>('main')
  const [search, setSearch] = useState('')
  const [choice, setChoice] = useState<ModelChoice | null>(null)
  const [changing, setChanging] = useState(false)
  const [draftEffortIndex, setDraftEffortIndex] = useState<number | null>(null)

  const options = useQuery({
    queryKey: ['astrorder', 'model-options', session.agent_id, session.id],
    queryFn: () => api.getSessionModels(session.id, session.agent_id),
    enabled: opened,
    staleTime: 30000,
    retry: false,
  })

  const isAntigravity = isAntigravityProvider(model.data?.provider)
    || Boolean(options.data?.items?.some((item) => isAntigravityProvider(item.provider)))
  const groupedOptions = useMemo(
    () => groupModelChoices(options.data?.items || []),
    [options.data?.items],
  )
  const parsedCurrent = useMemo(
    () => (isAntigravity ? parseAntigravityModelId(model.data?.model) : { baseModel: model.data?.model || '', effort: null }),
    [isAntigravity, model.data?.model],
  )
  const activeGroup = useMemo(
    () => findModelGroup(groupedOptions, model.data?.provider, model.data?.model),
    [groupedOptions, model.data?.provider, model.data?.model],
  )
  const effectiveEffort = normalizeEffortValue(model.effort) ?? parsedCurrent.effort
  const availableEfforts = useMemo(
    () => availableEffortsForModel(activeGroup, model.data?.supported_efforts),
    [activeGroup, model.data?.supported_efforts],
  )

  const modelOnly = isAntigravity
    ? (activeGroup
      ? (activeGroup.isGrouped
        ? false
        : activeGroup.efforts.length === 1
          ? true
          : model.data?.reasoning_read_only === true)
      : (parsedCurrent.effort !== null ? false : model.data?.reasoning_read_only === true))
    : model.data?.reasoning_read_only === true

  const applyGroup = async (group: ModelChoiceGroup) => {
    const target = resolveGroupVariant(group, effectiveEffort)
    setChoice(target)
    setChanging(true)
    try {
      await model.change(target.provider, target.model)
      setView('main')
      const nextHasEffort = isAntigravity
        ? group.isGrouped
        : !model.data?.reasoning_read_only
      if (!nextHasEffort) setOpened(false)
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
    const effortObj = availableEfforts[index]
    if (!effortObj || effortObj.value === effectiveEffort) {
      setDraftEffortIndex(null)
      return
    }
    if (changing) return
    setChanging(true)
    try {
      const variant = activeGroup?.isGrouped
        ? activeGroup.efforts.find((item) => item.effort === effortObj.value)?.choice
        : undefined
      if (variant) {
        setChoice(variant)
        await model.change(variant.provider, variant.model)
      } else {
        await model.changeEffort(effortObj.value)
      }
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

  const maxEffortIndex = Math.max(1, availableEfforts.length - 1)
  const resolvedEffortIndex = availableEfforts.findIndex((item) => item.value === effectiveEffort)
  const fallbackEffortIndex = availableEfforts.findIndex((item) => item.value === 'medium')
  const currentEffortIndex = resolvedEffortIndex >= 0
    ? resolvedEffortIndex
    : fallbackEffortIndex >= 0 ? fallbackEffortIndex : 0
  const sliderIndex = Math.min(maxEffortIndex, draftEffortIndex ?? currentEffortIndex)
  const activeEffortColor = EFFORT_COLOR_BY_VALUE[availableEfforts[sliderIndex]?.value] || '#3b82f6'
  const effortMarks = useMemo(
    () => availableEfforts.map((_, idx) => ({ value: idx, label: '' })),
    [availableEfforts],
  )
  const effortLabel = draftEffortIndex !== null || resolvedEffortIndex >= 0
    ? availableEfforts[sliderIndex]?.label
    : effortLabelOf(effectiveEffort) || model.effort || '未确认'

  const displayModelName = modelOnly && !model.data?.model
    ? '选择模型'
    : isAntigravity && model.data?.model && parsedCurrent.effort
      ? `${parsedCurrent.baseModel}${model.data.deferred ? ' · 下轮生效' : ''}`
      : formatDisplayModel(model.label)
  const cardModelName = activeGroup?.isGrouped
    ? `${activeGroup.label}${model.data?.deferred ? ' · 下轮生效' : ''}`
    : displayModelName
  const showPillEffort = Boolean(effortLabel) && (!modelOnly || Boolean(parsedCurrent.effort))
  const currentModelRoute = model.data?.model ? `${model.data.provider ? `${model.data.provider}/` : ''}${model.data.model}` : ''
  const effortControlDisabled = changing
    || Boolean(model.data?.read_only || model.data?.model_read_only)
    || (isAntigravity
      ? (!activeGroup?.isGrouped && (options.isPending || Boolean(model.data?.reasoning_read_only)))
      : Boolean(model.data?.reasoning_read_only))
  const searchQuery = search.trim().toLowerCase()

  return (
    <Popover
      opened={opened}
      onChange={(open) => {
        setOpened(open)
        if (!open) setView('main')
      }}
      hideDetached={false}
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
          title={displayModelName}
          onClick={() => {
            setOpened((v) => !v)
            setView('main')
          }}
        > 
          <span className="codex-model-pill-text">{displayModelName}</span>
          {!isOfficeMode && showPillQuota && <ModelQuotaBadge modelRoute={currentModelRoute} agentId={session.agent_id} agentKind={isAntigravity ? 'antigravity' : undefined} size="dot" />}
          {showPillEffort && <span className="codex-model-pill-effort">{effortLabel}</span>}
          <IconChevronDown size={14} className="codex-model-pill-arrow" />
        </button>
      </Popover.Target>

      <Popover.Dropdown className="codex-model-popover-dropdown">
        {(model.data?.read_only || model.data?.model_read_only) && <Text size="xs" c="dimmed" p="xs">{model.data.detail || '会话被其他客户端占用，当前只能读取模型信息。释放后点击刷新即可重新检查。'}</Text>}
        {view === 'main' && !modelOnly ? (
          <div className="codex-model-card" style={{ '--effort-color': activeEffortColor } as React.CSSProperties}>
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
                <span className="codex-model-name-label" title={model.label}>{cardModelName}</span>
              </button>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="sm"
                aria-label="刷新模型"
                onClick={() => { void model.refetch(); void options.refetch() }}
                loading={options.isFetching || model.isFetching}
              >
                <IconRefresh size={16} />
              </ActionIcon>
            </div>

            <div className={`codex-model-slider-wrap${sliderIndex === maxEffortIndex ? ' is-at-max' : ''}`}>
              <Slider
                disabled={effortControlDisabled}
                size={18}
                thumbSize={22}
                color={activeEffortColor}
                min={0}
                max={maxEffortIndex}
                step={1}
                marks={effortMarks}
                value={sliderIndex}
                thumbLabel="思考强度"
                onChange={setDraftEffortIndex}
                onChangeEnd={(val) => void applyEffortByIndex(val)}
                label={(val) => availableEfforts[val]?.label}
                styles={{
                  root: { width: '100%' },
                  track: {
                    borderRadius: 999,
                  },
                  bar: {
                    borderRadius: 999,
                    // Mantine extends both ends; intermediate fills must stop at the thumb.
                    width: sliderIndex === maxEffortIndex ? undefined : `calc(${(sliderIndex / maxEffortIndex) * 100}% + var(--slider-size))`,
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
              <div className="codex-model-effort-ticks">
                {availableEfforts.map((item, idx) => (
                  <button
                    key={item.value}
                    type="button"
                    className={`codex-model-effort-tick${idx === sliderIndex ? ' is-active' : ''}`}
                    disabled={effortControlDisabled}
                    onClick={() => void applyEffortByIndex(idx)}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            </div>
          </div>
        ) : (
          <div className="codex-model-list-panel">
            <div className="codex-model-list-head">
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  {!modelOnly && (
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size="xs"
                      aria-label="返回思考强度"
                      onClick={() => setView('main')}
                    >
                      <IconArrowLeft size={14} />
                    </ActionIcon>
                  )}
                  <Text size="xs" c="dimmed" fw={600}>
                    选择模型
                  </Text>
                </div>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="sm"
                  aria-label="刷新模型"
                  onClick={() => { void model.refetch(); void options.refetch() }}
                  loading={options.isFetching || model.isFetching}
                >
                  <IconRefresh size={16} />
                </ActionIcon>
              </div>
              {modelOnly && !model.data?.model && <Text size="xs" c="dimmed">原生未报告当前模型，请选择要使用的模型。</Text>}
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
              {groupedOptions
                .filter((item) =>
                  !searchQuery
                    || item.label.toLowerCase().includes(searchQuery)
                    || item.baseModel.toLowerCase().includes(searchQuery),
                )
                .map((item) => {
                  const isSelected = activeGroup
                    ? activeGroup.key === item.key
                    : choice
                      ? (choice.provider === item.provider && (
                        choice.model === item.defaultVariant.model
                        || choice.model === item.baseModel
                        || item.efforts.some((e) => e.choice.model === choice.model)
                      ))
                      : model.data
                        ? (model.data.provider ?? '') === (item.provider ?? '') && (
                          model.data.model === item.defaultVariant.model
                          || model.data.model === item.baseModel
                          || item.efforts.some((e) => e.choice.model === model.data?.model)
                        )
                        : model.label === item.defaultVariant.model
                          || model.label === `${item.provider ? `${item.provider}/` : ''}${item.defaultVariant.model}`

                  return (
                    <UnstyledButton
                      key={item.key}
                      className={"codex-model-menu-item" + (isSelected ? " is-selected" : "")}
                      onClick={() => void applyGroup(item)}
                      disabled={changing || (model.data?.read_only || model.data?.model_read_only)}
                    >
                      <span className="codex-model-menu-item-text" title={item.label}>{item.label}</span>
                      {!isOfficeMode && <ModelQuotaBadge modelRoute={`${item.provider ? `${item.provider}/` : ''}${item.defaultVariant.model}`} agentId={session.agent_id} agentKind={isAntigravity ? 'antigravity' : undefined} />}
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
