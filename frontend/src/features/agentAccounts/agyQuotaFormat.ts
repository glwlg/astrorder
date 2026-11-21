const stampFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
})
export const percentFormatter = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 })

export function agyStamp(value: string | number) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '' : stampFormatter.format(date)
}

export function windowOrder(window: string) {
  switch (window.toLowerCase()) {
    case '5h':
    case 'five_hour': return 0
    case 'weekly':
    case 'week': return 1
    default: return 2
  }
}

export function windowName(window: string, name: string) {
  return ['5 小时', '每周'][windowOrder(window)] ?? (name || window)
}
