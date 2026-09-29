import { useRef, useState } from 'react'
import { ActionIcon, Menu, Text, Tooltip } from '@mantine/core'
import { LayoutGroup, motion } from 'motion/react'
import {
  IconArrowLeft,
  IconArrowRight,
  IconDots,
  IconFolder,
  IconInfoCircle,
  IconTerminal2,
  IconWorld,
  IconX,
} from '@tabler/icons-react'
import { artifactViewerRegistry } from './registry'
import { useSidecarStore, type SidecarTab } from './sidecarStore'

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
  const [contextMenuTabId, setContextMenuTabId] = useState<string | null>(null)
  const [menuOpened, setMenuOpened] = useState(false)
  const [menuPosition, setMenuPosition] = useState<{ x: number; y: number }>({ x: 0, y: 0 })
  const tabsBarRef = useRef<HTMLDivElement>(null)

  const closeOtherTabs = useSidecarStore((state) => state.closeOtherTabs)
  const closeTabsToRight = useSidecarStore((state) => state.closeTabsToRight)
  const closeTabsToLeft = useSidecarStore((state) => state.closeTabsToLeft)
  const closeAllTabs = useSidecarStore((state) => state.closeAllTabs)

  const handleWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (!tabsBarRef.current) return
    // 如果存在垂直滚动量但没有水平滚动量，转换为水平滚动
    if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
      e.preventDefault()
      tabsBarRef.current.scrollLeft += e.deltaY
    }
  }

  if (tabs.length === 0) return null

  const targetIndex = contextMenuTabId ? tabs.findIndex((t) => t.id === contextMenuTabId) : -1
  const canCloseLeft = targetIndex > 0
  const canCloseRight = targetIndex >= 0 && targetIndex < tabs.length - 1
  const canCloseOther = tabs.length > 1
  const targetTab = contextMenuTabId ? tabs.find((t) => t.id === contextMenuTabId) : null

  return (
    <LayoutGroup id="sidecar-tabs-nav">
    <div
      ref={tabsBarRef}
      onWheel={handleWheel}
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
            onContextMenu={(e) => {
              e.preventDefault()
              e.stopPropagation()
              setContextMenuTabId(tab.id)
              setMenuPosition({ x: e.clientX, y: e.clientY })
              setMenuOpened(true)
            }}
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

    {/* 右键上下文菜单 */}
    <Menu
      opened={menuOpened}
      onChange={setMenuOpened}
      withinPortal
      shadow="md"
      width={160}
      position="bottom-start"
    >
      <Menu.Target>
        <span
          style={{
            position: 'fixed',
            left: menuPosition.x,
            top: menuPosition.y,
            width: 1,
            height: 1,
            pointerEvents: 'none',
          }}
        />
      </Menu.Target>
      <Menu.Dropdown>
        {targetTab?.closable && (
          <Menu.Item
            leftSection={<IconX size={14} />}
            onClick={() => {
              if (contextMenuTabId) onClose(contextMenuTabId)
            }}
          >
            关闭标签页
          </Menu.Item>
        )}
        <Menu.Item
          leftSection={<IconDots size={14} />}
          disabled={!canCloseOther}
          onClick={() => {
            if (contextMenuTabId) closeOtherTabs(contextMenuTabId)
          }}
        >
          关闭其他标签页
        </Menu.Item>
        <Menu.Item
          leftSection={<IconArrowRight size={14} />}
          disabled={!canCloseRight}
          onClick={() => {
            if (contextMenuTabId) closeTabsToRight(contextMenuTabId)
          }}
        >
          关闭右侧标签页
        </Menu.Item>
        <Menu.Item
          leftSection={<IconArrowLeft size={14} />}
          disabled={!canCloseLeft}
          onClick={() => {
            if (contextMenuTabId) closeTabsToLeft(contextMenuTabId)
          }}
        >
          关闭左侧标签页
        </Menu.Item>
        <Menu.Divider />
        <Menu.Item
          color="red"
          leftSection={<IconX size={14} />}
          onClick={() => closeAllTabs()}
        >
          关闭全部标签页
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
    </LayoutGroup>
  )
}
