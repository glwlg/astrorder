import type { AgyQuota } from './agyAccountsApi'
import { agyStamp, percentFormatter, windowName, windowOrder } from './agyQuotaFormat'

export function AgyQuotaTiles({ quota }: { quota: AgyQuota }) {
  const tiles = quota.groups.flatMap((group, groupIndex) => [...group.buckets]
    .sort((a, b) => windowOrder(a.window) - windowOrder(b.window)).map((bucket, index) => {
    const window = windowName(bucket.window, bucket.name)
    const label = window && group.name !== window ? `${group.name} · ${window}` : group.name
    return { ...bucket, label, key: `${groupIndex}-${bucket.id}-${index}` }
  }))
  return <div className="agy-quota-tiles">
    {tiles.map(({ key, label, remaining_fraction: remaining, reset_at: reset }) => {
      const percent = percentFormatter.format(remaining)
      const tone = remaining === 0 ? 'empty' : remaining <= 0.25 ? 'low' : 'available'
      const resetLabel = reset ? agyStamp(reset) : ''
      return <div className="agy-quota-tile" key={key} data-tone={tone}>
        <div className="agy-quota-tile-heading"><span>{label}</span><strong>{percent}</strong></div>
        <meter min={0} max={1} value={remaining} aria-label={`${label}剩余额度`} aria-valuetext={percent} />
        {resetLabel && <time dateTime={new Date(reset!).toISOString()}>{resetLabel} 重置</time>}
      </div>
    })}
  </div>
}
