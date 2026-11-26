import type { AgyQuota, AgyQuotaBucket } from './agyAccountsApi'
import { agyStamp, percentFormatter, windowName, windowOrder } from './agyQuotaFormat'

const groupLabels: Record<string, string> = { 'Gemini Models': 'Gemini', 'Claude and GPT models': 'Claude / GPT' }

type PeriodEntry = { group: string; color: number; bucket: AgyQuotaBucket }

function pickPeriodReset(entries: PeriodEntry[]): { resetAt: number; label: string; title: string } | null {
  const withReset = entries
    .map((entry) => {
      const resetAt = entry.bucket.reset_at
      if (typeof resetAt !== 'number' || !Number.isFinite(resetAt) || resetAt <= 0) return null
      const label = agyStamp(resetAt)
      if (!label) return null
      return {
        groupLabel: groupLabels[entry.group] ?? entry.group,
        remaining: entry.bucket.remaining_fraction,
        resetAt,
        label,
      }
    })
    .filter((item): item is NonNullable<typeof item> => item !== null)

  if (!withReset.length) return null

  // Prefer the most consumed group's reset time, breaking ties by earliest reset.
  const sorted = [...withReset].sort((a, b) => a.remaining - b.remaining || a.resetAt - b.resetAt)
  const primary = sorted[0]
  const distinctLabels = new Set(withReset.map((item) => item.label))
  const title = distinctLabels.size > 1
    ? withReset.map((item) => `${item.groupLabel} ${item.label} 重置`).join(' · ')
    : `${primary.label} 重置`

  return { resetAt: primary.resetAt, label: primary.label, title }
}

export function AgyQuotaSummary({ quota }: { quota: AgyQuota }) {
  const periods = new Map<string, PeriodEntry[]>()
  quota.groups.forEach(({ name, buckets }, index) => {
    for (const bucket of buckets) {
      const period = windowName(bucket.window, bucket.name)
      const entries = periods.get(period) ?? []
      entries.push({ group: name, color: index % 3, bucket })
      periods.set(period, entries)
    }
  })
  const rows = [...periods].sort(([, a], [, b]) => windowOrder(a[0].bucket.window) - windowOrder(b[0].bucket.window))
  if (!rows.length) return <small>{quota.detail || quota.account_label || '暂无额度数据'}</small>
  return <div className="session-account-quotas">{rows.map(([period, entries]) => {
    const reset = pickPeriodReset(entries)
    return <div className="session-account-period" role="group" aria-label={`${period}额度`} key={period}>
      <small>{period}</small>
      <div className="session-account-period-values">{entries.map(({ group, color, bucket }) => {
        const remaining = bucket.remaining_fraction
        const percent = percentFormatter.format(remaining)
        const label = `${group} · ${period}剩余额度`
        const details = [label, percent, bucket.reset_at && `${agyStamp(bucket.reset_at)} 重置`, `更新于 ${agyStamp(quota.as_of)}`]
        return <span key={`${group}-${bucket.id}`} className="session-account-quota" data-color={color} role="meter"
          aria-label={label} aria-valuemin={0} aria-valuemax={1} aria-valuenow={remaining}
          aria-valuetext={percent} title={details.filter(Boolean).join(' · ')}>
          <span>{groupLabels[group] ?? group}</span>
          <b data-tone={remaining === 0 ? 'empty' : remaining <= .25 ? 'low' : undefined}>{percent}</b>
        </span>
      })}</div>
      {reset && <time className="session-account-reset" dateTime={new Date(reset.resetAt).toISOString()} title={reset.title}>{reset.label} 重置</time>}
    </div>
  })}
    {quota.detail && <small className="session-account-detail">{quota.detail}</small>}
  </div>
}
