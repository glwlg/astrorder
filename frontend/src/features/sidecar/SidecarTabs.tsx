import { ActionIcon, Text, Tooltip } from '@mantine/core'
import { LayoutGroup, motion } from 'motion/react'
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
    <LayoutGroup id="sidecar-tabs-nav">
    <div
      className="sidecar-tabs-bar"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '2px',
        overflowX: 'auto',
        maxWidth: '100%',
        padding: '3px',
        background: 'color-mix(in srgb, var(--astr-surface-muted) 85%, var(--astr-surface))',
        borderRadius: '8px',
        border: '1px solid var(--astr-border)',
      }}
    >
      {tabs.map((tab) => {
        const isActive = tab.id === activeId
        const isDirty = dirtyTabs[tab.id]
        const viewer = tab.artifact ? artifactViewerRegistry.findViewer(tab.artifact) : null

        return (
          <div
            key={tab.id}
            style={{
              position: 'relative',
              display: 'inline-flex',
            }}
            onClick={() => onSelect(tab.id)}
          >
            {isActive && (
              <motion.div
                layoutId="sidecar-active-tab-pill"
                style={{
                  position: 'absolute',
                  inset: 0,
                  borderRadius: '6px',
                  background: 'var(--astr-surface)',
                  border: '1px solid var(--astr-border)',
                  boxShadow: '0 1px 4px rgba(0, 0, 0, 0.1), 0 1px 2px rgba(0, 0, 0, 0.06)',
                  zIndex: 0,
                }}
                transition={{ type: 'spring', stiffness: 400, damping: 28 }}
              />
            )}
            <div
              style={{
                position: 'relative',
                zIndex: 1,
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                padding: '4px 9px',
                cursor: 'pointer',
                fontSize: '12px',
                userSelect: 'none',
                whiteSpace: 'nowrap',
              }}
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
            <Text size="xs" fw={isActive ? 650 : 450} c={isActive ? 'var(--astr-text)' : 'dimmed'}>
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
            </div>
          </div>
        )
      })}
    </div>
    </LayoutGroup>
  )
}
