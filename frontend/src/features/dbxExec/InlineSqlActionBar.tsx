import { Menu } from '@mantine/core'
import {
  IconCheck,
  IconChevronDown,
  IconDatabase,
  IconLoader2,
  IconPlayerPlay,
  IconPlayerStop,
  IconSettings,
} from '@tabler/icons-react'
import type { ProjectDatabaseTarget } from '../../api/client'
import { formatTargetLabel } from './projectDbStore'
import './inlineSql.css'

export function InlineSqlActionBar({
  targets,
  activeTarget,
  running,
  onSelectTarget,
  onOpenBindModal,
  onRun,
  onCancel,
}: {
  targets: ProjectDatabaseTarget[]
  activeTarget: ProjectDatabaseTarget | null
  running: boolean
  onSelectTarget: (targetId: string) => void
  onOpenBindModal: () => void
  onRun: () => void
  onCancel: () => void
}) {
  const isProd = Boolean(activeTarget?.is_prod)
  const targetLabel = activeTarget ? formatTargetLabel(activeTarget) : '关联数据库'

  return (
    <>
      {targets.length > 0 ? (
        <Menu position="bottom-end" shadow="md" width={260} withinPortal>
          <Menu.Target>
            <button
              type="button"
              className={`inline-sql-target-btn${isProd ? ' is-prod' : ''}`}
              onClick={(e) => e.stopPropagation()}
              aria-label={`当前目标数据库：${targetLabel}${isProd ? '（生产环境）' : ''}`}
              title={`当前目标：${targetLabel}${isProd ? ' [PROD]' : ''}\n点击切换或管理本项目数据库`}
            >
              <IconDatabase size={12} aria-hidden="true" style={{ flexShrink: 0 }} />
              <span className="inline-sql-target-label">{targetLabel}</span>
              {isProd && <span className="inline-sql-prod-badge">PROD</span>}
              <IconChevronDown size={11} aria-hidden="true" style={{ flexShrink: 0, opacity: 0.7 }} />
            </button>
          </Menu.Target>
          <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
            <Menu.Label>本项目已关联数据库</Menu.Label>
            {targets.map((target) => {
              const selected = activeTarget?.id === target.id
              return (
                <Menu.Item
                  key={target.id}
                  leftSection={selected ? <IconCheck size={14} /> : <IconDatabase size={14} />}
                  rightSection={target.is_prod ? <span className="inline-sql-prod-badge">PROD</span> : undefined}
                  onClick={() => onSelectTarget(target.id)}
                >
                  {formatTargetLabel(target)}
                </Menu.Item>
              )
            })}
            <Menu.Divider />
            <Menu.Item leftSection={<IconSettings size={14} />} onClick={onOpenBindModal}>
              管理项目数据库关联…
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      ) : (
        <button
          type="button"
          className="inline-sql-target-btn"
          onClick={(e) => {
            e.stopPropagation()
            onOpenBindModal()
          }}
          aria-label="关联项目数据库"
          title="为当前项目关联 DBX 数据库后即可原地执行 SQL"
        >
          <IconDatabase size={12} aria-hidden="true" />
          <span className="inline-sql-target-label">关联数据库</span>
        </button>
      )}

      {running ? (
        <button
          type="button"
          className="inline-sql-run-btn is-running"
          onClick={(e) => {
            e.stopPropagation()
            onCancel()
          }}
          aria-label="取消执行 SQL"
        >
          <IconLoader2 size={12} className="astrorder-spin" aria-hidden="true" />
          <IconPlayerStop size={11} aria-hidden="true" />
          <span>取消</span>
        </button>
      ) : (
        <button
          type="button"
          className={`inline-sql-run-btn${isProd ? ' is-prod' : ''}`}
          onClick={(e) => {
            e.stopPropagation()
            onRun()
          }}
          aria-label="原地执行此 SQL"
          title={
            !activeTarget
              ? '先为项目关联目标数据库后执行'
              : `在 ${targetLabel}${isProd ? ' [PROD]' : ''} 原地执行（纯只读查询一键直出，事务/写入/DDL 自动弹窗确认）`
          }
        >
          <IconPlayerPlay size={11} aria-hidden="true" />
          <span>执行</span>
        </button>
      )}
    </>
  )
}
