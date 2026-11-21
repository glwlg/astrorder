import type { AgyQuota } from './agyAccountsApi'
import { agyStamp, percentFormatter, windowName, windowOrder } from './agyQuotaFormat'

const groupLabels: Record<string, string> = { 'Gemini Models': 'Gemini', 'Claude and GPT models': 'Claude / GPT' }

export function AgyQuotaSummary({ quota }: { quota: AgyQuota }) {
  const items = quota.groups.flatMap(({ name, buckets }) => {
    const bucket = [...buckets].sort((a, b) => windowOrder(a.window) - windowOrder(b.window))[0]
    if (!bucket) return []
    const window = windowName(bucket.window, bucket.name)
    const percent = percentFormatter.format(bucket.remaining_fraction)
    const label = `${name} · ${window}剩余额度`
    const details = [label, percent, bucket.reset_at && `${agyStamp(bucket.reset_at)} 重置`, `更新于 ${agyStamp(quota.as_of)}`]
    return <span key={`${name}-${bucket.id}`} className="session-account-quota" role="meter"
      aria-label={label} aria-valuemin={0} aria-valuemax={1} aria-valuenow={bucket.remaining_fraction}
      aria-valuetext={percent} title={details.filter(Boolean).join(' · ')}>
      <span>{groupLabels[name] ?? name}</span>
      <b data-low={bucket.remaining_fraction <= .25 || undefined}>{percent}</b>
      <small>{window === '5 小时' ? '5h' : window === '每周' ? '周' : window}</small>
    </span>
  })
  return items.length ? <div className="session-account-quotas">{items}</div> : <small>暂无额度数据</small>
}
