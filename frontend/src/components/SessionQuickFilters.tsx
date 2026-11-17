import type { ReactNode } from 'react'
import type { RailFilter } from './sessionRailModel'

export const SESSION_QUICK_FILTERS = [
  ['all', '全部'],
  ['running', '运行中'],
  ['unread', '未读'],
  ['pinned', '置顶'],
  ['recent', '24小时'],
] as const

export function SessionQuickFilters({ value, onChange, children }: {
  value: RailFilter
  onChange: (value: RailFilter) => void
  children?: ReactNode
}) {
  return (
    <div className="session-quick-filter-row">
      <div className="session-quick-filters" role="group" aria-label="会话快速筛选">
        {SESSION_QUICK_FILTERS.map(([key, label]) => (
          <button key={key} type="button" className="session-quick-filter"
            aria-pressed={key === value} onClick={() => onChange(key)}>
            {label}
          </button>
        ))}
      </div>
      {children}
    </div>
  )
}
