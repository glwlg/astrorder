import type { AgyQuota, AgyQuotaBucket } from './agyAccountsApi'
import { agyStamp, percentFormatter, windowName, windowOrder } from './agyQuotaFormat'

const groupLabels: Record<string, string> = { 'Gemini Models': 'Gemini', 'Claude and GPT models': 'Claude / GPT' }

export function AgyQuotaSummary({ quota }: { quota: AgyQuota }) {
  type Entry = { group: string; color: number; bucket: AgyQuotaBucket }
  const periods = new Map<string, Entry[]>()
  quota.groups.forEach(({ name, buckets }, index) => {
    for (const bucket of buckets) {
      const period = windowName(bucket.window, bucket.name)
      const entries = periods.get(period) ?? []
      entries.push({ group: name, color: index % 3, bucket })
      periods.set(period, entries)
    }
  })
  const rows = [...periods].sort(([, a], [, b]) => windowOrder(a[0].bucket.window) - windowOrder(b[0].bucket.window))
  if (!rows.length) return <small>暂无额度数据</small>
  return <div className="session-account-quotas">{rows.map(([period, entries]) =>
    <div className="session-account-period" role="group" aria-label={`${period}额度`} key={period}>
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
    </div>
  )}</div>
}
