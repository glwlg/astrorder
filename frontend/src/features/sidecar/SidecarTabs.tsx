import { ActionIcon, Paper, Text, Tooltip } from '@mantine/core'
import {
  IconFolder,
  IconInfoCircle,
  IconTerminal2,
  IconWorld,
  IconX,
} from '@tabler/icons-react'
import { artifactViewerRegistry } from './registry'
import type { SidecarTab } from './sidecarStore'

interface SidecarTabsProps {
  tabs: SidecarTab[]
  activeId: string
  dirtyTabs: Record<string, boolean>
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
}

export function SidecarTabs({
  tabs,
  activeId,
  dirtyTabs,
  onSelect,
  onClose,
}: SidecarTabsProps) {
  if (tabs.length === 0) return null

  return (
    <div
      className="sidecar-tabs-bar"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '4px',
        overflowX: 'auto',
        maxWidth: '100%',
        padding: '2px 0',
      }}
    >
      {tabs.map((tab) => {
        const isActive = tab.id === activeId
        const isDirty = dirtyTabs[tab.id]
        const viewer = tab.artifact ? artifactViewerRegistry.findViewer(tab.artifact) : null

        return (
          <Paper
            key={tab.id}
            px={8}
            py={3}
            radius="sm"
            withBorder={isActive}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              cursor: 'pointer',
              background: isActive ? 'var(--astr-card)' : 'transparent',
              borderColor: isActive ? 'var(--astr-border)' : 'transparent',
              fontSize: '12px',
              userSelect: 'none',
              transition: 'background 0.1s ease',
              whiteSpace: 'nowrap',
            }}
            onClick={() => onSelect(tab.id)}
          >
            {/* 图标 */}
            {tab.type === 'details' ? (
              <IconInfoCircle size={14} color="var(--astr-muted)" />
            ) : tab.id.startsWith('filetree:') ? (
              <IconFolder size={14} color="var(--astr-blue, #3b82f6)" />
            ) : tab.id.startsWith('terminal:') ? (
              <IconTerminal2 size={14} color="var(--astr-green, #10b981)" />
            ) : tab.id.startsWith('browser:') ? (
              <IconWorld size={14} color="var(--astr-cyan, #06b6d4)" />
            ) : viewer?.icon ? (
              <viewer.icon size={14} />
            ) : (
              <IconInfoCircle size={14} color="var(--astr-muted)" />
            )}

            {/* 标题 */}
            <Text size="xs" fw={isActive ? 600 : 400} c={isActive ? undefined : 'dimmed'}>
              {tab.title}
            </Text>

            {/* 未保存脏标记点 */}
            {isDirty && (
              <Tooltip label="有未保存的修改">
                <span
                  style={{
                    width: 6,
                    height: 6,
                    borderRadius: '50%',
                    background: 'var(--astr-warning, #f59e0b)',
                    display: 'inline-block',
                  }}
                />
              </Tooltip>
            )}

            {/* 关闭按钮 */}
            {tab.closable && (
              <ActionIcon
                size={18}
                variant="subtle"
                color="gray"
                onClick={(e) => {
                  e.stopPropagation()
                  onClose(tab.id)
                }}
                style={{ marginLeft: 2, flexShrink: 0 }}
                title="关闭标签页"
              >
                <IconX size={12} />
              </ActionIcon>
            )}
          </Paper>
        )
      })}
    </div>
  )
}
