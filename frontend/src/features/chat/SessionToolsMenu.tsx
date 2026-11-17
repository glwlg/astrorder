import { ActionIcon, Menu } from '@mantine/core'
import { BrainIcon, ChalkboardSimpleIcon, DotsThreeVerticalIcon } from '../../components/workbenchIcons'
import type { Session } from '../../domain/types'
import { useSidecarStore } from '../sidecar/sidecarStore'

export function SessionToolsMenu({ session }: {
  session: Session
}) {
  return (
    <Menu position="bottom-end" width={240}>
      <Menu.Target>
        <ActionIcon variant="subtle" className="session-tools-button" aria-label="更多会话工具">
          <DotsThreeVerticalIcon size={22} weight="bold" aria-hidden="true" />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Item leftSection={<BrainIcon size={19} />} onClick={() => useSidecarStore.getState().openMemory(session.id)}>
          知识记忆看板
        </Menu.Item>
        <Menu.Item leftSection={<ChalkboardSimpleIcon size={19} />} onClick={() => useSidecarStore.getState().openBlackboard(session.id, session.agent_id, session.connection_id || undefined)}>
          作战黑板
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )
}
