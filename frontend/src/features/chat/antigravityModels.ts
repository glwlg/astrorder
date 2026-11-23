import { REASONING_EFFORTS } from './composerMedia'

export type ReasoningEffortValue = (typeof REASONING_EFFORTS)[number]['value']

export interface ModelChoice {
  provider: string
  model: string
  label: string
}

export interface ModelChoiceEffortVariant {
  effort: ReasoningEffortValue
  label: string
  choice: ModelChoice
}

export interface ModelChoiceGroup {
  key: string
  provider: string
  baseModel: string
  label: string
  /** Concrete choice used for quota badge lookup and default selection */
  defaultVariant: ModelChoice
  /** Concrete variants sorted in canonical REASONING_EFFORTS order (low -> medium -> high -> xhigh -> max) */
  efforts: ModelChoiceEffortVariant[]
  /** True when multiple Antigravity variants were collapsed into this base model */
  isGrouped: boolean
}

const EFFORT_ORDER: readonly ReasoningEffortValue[] = REASONING_EFFORTS.map((item) => item.value)
const EFFORT_LABEL_MAP: Record<string, string> = Object.fromEntries(
  REASONING_EFFORTS.map((item) => [item.value, item.label]),
)
const MODEL_EFFORT_SUFFIX_RE = /^(.+)-(xhigh|low|medium|high|max)$/i
const LABEL_EFFORT_SUFFIX_RE = /^(.*?)\s*\((xhigh|low|medium|high|max)\)\s*$/i

export function normalizeEffortValue(value?: string | null): ReasoningEffortValue | null {
  const lower = (value || '').trim().toLowerCase()
  return (EFFORT_ORDER as readonly string[]).includes(lower) ? (lower as ReasoningEffortValue) : null
}

export function effortLabelOf(value?: string | null): string | null {
  const norm = normalizeEffortValue(value)
  return norm ? EFFORT_LABEL_MAP[norm] : null
}

export function isAntigravityProvider(provider?: string | null): boolean {
  const lower = (provider || '').trim().toLowerCase()
  return lower === 'antigravity' || lower === 'antigravity-ssh'
}

export function parseAntigravityModelId(modelId?: string | null): {
  baseModel: string
  effort: ReasoningEffortValue | null
} {
  const trimmed = (modelId || '').trim()
  if (!trimmed) return { baseModel: '', effort: null }
  const match = MODEL_EFFORT_SUFFIX_RE.exec(trimmed)
  if (!match) return { baseModel: trimmed, effort: null }
  return {
    baseModel: match[1],
    effort: normalizeEffortValue(match[2]),
  }
}

export function parseAntigravityModelChoice(choice: ModelChoice): {
  baseModel: string
  baseLabel: string
  effort: ReasoningEffortValue | null
} {
  const idParsed = parseAntigravityModelId(choice.model)
  const labelMatch = LABEL_EFFORT_SUFFIX_RE.exec(choice.label.trim())
  const labelEffort = labelMatch ? normalizeEffortValue(labelMatch[2]) : null
  const effort = idParsed.effort ?? labelEffort
  const baseLabel = labelMatch && (!idParsed.effort || idParsed.effort === labelEffort)
    ? labelMatch[1].trim() || choice.label.trim()
    : choice.label.trim()
  return {
    baseModel: idParsed.baseModel,
    baseLabel,
    effort,
  }
}

export function groupModelChoices(
  items: ModelChoice[],
  options?: { treatAllAsAntigravity?: boolean },
): ModelChoiceGroup[] {
  const result: ModelChoiceGroup[] = []
  const byKey = new Map<string, {
    index: number
    provider: string
    baseModel: string
    baseLabel: string
    variants: Array<{ effort: ReasoningEffortValue | null; choice: ModelChoice }>
  }>()

  for (const item of items) {
    const isAgy = options?.treatAllAsAntigravity || isAntigravityProvider(item.provider)
    if (!isAgy) {
      result.push({
        key: JSON.stringify([item.provider, item.model]),
        provider: item.provider,
        baseModel: item.model,
        label: item.label,
        defaultVariant: item,
        efforts: [],
        isGrouped: false,
      })
      continue
    }

    const parsed = parseAntigravityModelChoice(item)
    if (!parsed.effort) {
      result.push({
        key: JSON.stringify([item.provider, item.model]),
        provider: item.provider,
        baseModel: item.model,
        label: item.label,
        defaultVariant: item,
        efforts: [],
        isGrouped: false,
      })
      continue
    }

    const groupKey = `${item.provider.toLowerCase()}::${parsed.baseModel.toLowerCase()}`
    let bucket = byKey.get(groupKey)
    if (!bucket) {
      bucket = {
        index: result.length,
        provider: item.provider,
        baseModel: parsed.baseModel,
        baseLabel: parsed.baseLabel,
        variants: [],
      }
      byKey.set(groupKey, bucket)
      result.push({
        key: JSON.stringify([item.provider, parsed.baseModel]),
        provider: item.provider,
        baseModel: parsed.baseModel,
        label: parsed.baseLabel,
        defaultVariant: item,
        efforts: [],
        isGrouped: false,
      })
    }
    bucket.variants.push({ effort: parsed.effort, choice: item })
  }

  for (const bucket of byKey.values()) {
    const uniqueEfforts = new Map<ReasoningEffortValue, ModelChoice>()
    for (const v of bucket.variants) {
      if (v.effort && !uniqueEfforts.has(v.effort)) {
        uniqueEfforts.set(v.effort, v.choice)
      }
    }
    const orderedEfforts: ModelChoiceEffortVariant[] = EFFORT_ORDER
      .filter((effort) => uniqueEfforts.has(effort))
      .map((effort) => ({
        effort,
        label: EFFORT_LABEL_MAP[effort],
        choice: uniqueEfforts.get(effort)!,
      }))

    if (orderedEfforts.length <= 1) {
      const single = bucket.variants[0].choice
      result[bucket.index] = {
        key: JSON.stringify([single.provider, single.model]),
        provider: single.provider,
        baseModel: bucket.baseModel,
        label: single.label,
        defaultVariant: single,
        efforts: orderedEfforts,
        isGrouped: false,
      }
    } else {
      const defaultVariant =
        uniqueEfforts.get('high') ??
        uniqueEfforts.get('medium') ??
        bucket.variants[0].choice
      result[bucket.index] = {
        key: JSON.stringify([bucket.provider, bucket.baseModel]),
        provider: bucket.provider,
        baseModel: bucket.baseModel,
        label: bucket.baseLabel,
        defaultVariant,
        efforts: orderedEfforts,
        isGrouped: true,
      }
    }
  }

  return result
}

export function findModelGroup(
  groups: ModelChoiceGroup[],
  provider?: string | null,
  modelId?: string | null,
): ModelChoiceGroup | null {
  if (!modelId) return null
  const prov = provider ?? ''
  for (const group of groups) {
    if ((group.provider ?? '') !== prov) continue
    if (group.defaultVariant.model === modelId || group.baseModel === modelId) {
      return group
    }
    if (group.efforts.some((e) => e.choice.model === modelId)) {
      return group
    }
  }
  return null
}

export function resolveGroupVariant(
  group: ModelChoiceGroup,
  preferredEffort?: string | null,
): ModelChoice {
  if (!group.efforts.length) return group.defaultVariant
  const wanted = normalizeEffortValue(preferredEffort)
  if (wanted) {
    const exact = group.efforts.find((item) => item.effort === wanted)
    if (exact) return exact.choice
    const wantedIdx = EFFORT_ORDER.indexOf(wanted)
    let best = group.efforts[0]
    let bestDist = Math.abs(EFFORT_ORDER.indexOf(best.effort) - wantedIdx)
    for (const candidate of group.efforts.slice(1)) {
      const dist = Math.abs(EFFORT_ORDER.indexOf(candidate.effort) - wantedIdx)
      if (
        dist < bestDist ||
        (dist === bestDist && EFFORT_ORDER.indexOf(candidate.effort) > EFFORT_ORDER.indexOf(best.effort))
      ) {
        best = candidate
        bestDist = dist
      }
    }
    return best.choice
  }
  return group.defaultVariant
}

export function availableEffortsForModel(
  group: ModelChoiceGroup | null,
  supportedEfforts?: string[] | null,
): ReadonlyArray<{ value: ReasoningEffortValue; label: string }> {
  if (group?.isGrouped && group.efforts.length > 1) {
    return group.efforts.map((item) => ({ value: item.effort, label: item.label }))
  }
  if (supportedEfforts && supportedEfforts.length > 0) {
    const allowed = new Set(supportedEfforts.map((v) => v.trim().toLowerCase()))
    const filtered = REASONING_EFFORTS.filter((item) => allowed.has(item.value))
    if (filtered.length > 0) return filtered
  }
  return REASONING_EFFORTS
}
