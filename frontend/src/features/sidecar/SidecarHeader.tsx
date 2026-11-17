import { ActionIcon, Tooltip } from '@mantine/core'
import { PlusIcon, SidebarSimpleIcon } from '../../components/workbenchIcons'
import { SidecarTabs } from './SidecarTabs'
import type { SidecarTab } from './sidecarStore'
import './SidecarHeader.css'

interface SidecarHeaderProps {
  tabs: SidecarTab[]
  activeId: string
  dirtyTabs: Record<string, boolean>
  onSelect: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onNewPanel?: () => void
  onClose?: () => void
  closeLabel?: string
}

export function SidecarHeader({
  tabs, activeId, dirtyTabs, onSelect, onCloseTab, onNewPanel, onClose,
  closeLabel = '收起工作台',
}: SidecarHeaderProps) {
  return (
    <div className="sidecar-header-bar">
      <div className="sidecar-header-tabs">
        <SidecarTabs tabs={tabs} activeId={activeId} dirtyTabs={dirtyTabs} onSelect={onSelect} onClose={onCloseTab} />
        {onNewPanel && (
          <Tooltip label="新建面板">
            <ActionIcon className="sidecar-header-action" variant="subtle"
              onClick={onNewPanel} aria-label="新建面板">
              <PlusIcon aria-hidden="true" />
            </ActionIcon>
          </Tooltip>
        )}
      </div>
      {onClose && (
        <Tooltip label={closeLabel}>
          <ActionIcon className="sidecar-header-action" variant="subtle"
            onClick={onClose} aria-label={closeLabel}>
            <SidebarSimpleIcon mirrored weight="duotone" aria-hidden="true" />
          </ActionIcon>
        </Tooltip>
      )}
    </div>
  )
}
