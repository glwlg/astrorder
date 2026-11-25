import { Title, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconBriefcase } from '@tabler/icons-react'
import { FolderSimpleIcon } from '../../components/workbenchIcons'
import type { Session } from '../../domain/types'
import { displaySessionTitle } from '../../components/sessionRailModel'
import { useAppProfile, useIsOfficeExperience } from '../office/appProfile'
import { setProjectOfficeMode } from '../office/officeWorkspaceMode'

export function SessionBreadcrumb({ session }: { session: Session }) {
  const project = session.project_name || session.workspace?.replaceAll('\\', '/').split('/').filter(Boolean).at(-1)
  const appProfile = useAppProfile()
  const isOfficeExperience = useIsOfficeExperience(session)
  const canTogglePerProject = appProfile !== 'office' && isOfficeExperience

  const handleExitProjectOfficeMode = () => {
    const projKey = session.project_id
      ? `project:${session.source_id || 'local'}\u0000${session.project_id}`
      : ''
    setProjectOfficeMode({ projectKey: projKey, workspace: session.worktree_root_workspace || session.workspace }, false)
    notifications.show({
      color: 'teal',
      message: `已将「${project || '当前工作区'}」切换回开发项目`,
    })
  }

  return (
    <div className="session-breadcrumb">
      <span className="session-breadcrumb-icon" aria-hidden="true"><FolderSimpleIcon size={20} weight="duotone" /></span>
      <div className="session-breadcrumb-copy">
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
          {project && <span className="session-breadcrumb-project" title={session.workspace || undefined}>{project}</span>}
          {isOfficeExperience && (
            canTogglePerProject ? (
              <Tooltip label="当前为办公空间（点击切回开发项目）" position="bottom" withArrow>
                <button
                  type="button"
                  onClick={handleExitProjectOfficeMode}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 3,
                    padding: '1px 6px',
                    borderRadius: 999,
                    border: '1px solid rgba(14, 165, 233, 0.28)',
                    background: 'rgba(14, 165, 233, 0.1)',
                    color: '#0369a1',
                    fontSize: 10.5,
                    fontWeight: 600,
                    cursor: 'pointer',
                    flexShrink: 0,
                  }}
                >
                  <IconBriefcase size={10} />
                  <span>办公空间</span>
                </button>
              </Tooltip>
            ) : (
              <span
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 3,
                  padding: '1px 6px',
                  borderRadius: 999,
                  background: 'var(--astr-surface-muted, #f4f4f5)',
                  color: 'var(--astr-muted, #71717a)',
                  fontSize: 10.5,
                  fontWeight: 600,
                  flexShrink: 0,
                }}
              >
                <IconBriefcase size={10} />
                <span>办公空间</span>
              </span>
            )
          )}
        </div>
        <Title order={2} className="chat-title-text">{displaySessionTitle(session)}</Title>
      </div>
    </div>
  )
}
