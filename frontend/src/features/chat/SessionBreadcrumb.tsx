import { Title } from '@mantine/core'
import { FolderSimpleIcon } from '../../components/workbenchIcons'
import type { Session } from '../../domain/types'
import { displaySessionTitle } from '../../components/sessionRailModel'

export function SessionBreadcrumb({ session }: { session: Session }) {
  const project = session.project_name || session.workspace?.replaceAll('\\', '/').split('/').filter(Boolean).at(-1)
  return (
    <div className="session-breadcrumb">
      <span className="session-breadcrumb-icon" aria-hidden="true"><FolderSimpleIcon size={20} weight="duotone" /></span>
      <div className="session-breadcrumb-copy">
        {project && <span className="session-breadcrumb-project" title={session.workspace || undefined}>{project}</span>}
        <Title order={2} className="chat-title-text">{displaySessionTitle(session)}</Title>
      </div>
    </div>
  )
}
