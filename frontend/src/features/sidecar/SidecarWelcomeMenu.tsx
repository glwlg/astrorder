import { Text, UnstyledButton } from '@mantine/core'
import {
  IconBrain,
  IconFolder,
  IconChalkboard,
  IconMessages,
  IconSparkles,
  IconTerminal2,
  IconWorld,
} from '@tabler/icons-react'
import type { Session } from '../../domain/types'

interface SidecarWelcomeMenuProps {
  session: Session
  onOpenFileTree: () => void
  onOpenTerminal: () => void
  onOpenBrowser: () => void
  onOpenSideChat: () => void
  onOpenAgentGraph: () => void
  onOpenBlackboard?: () => void
}

export function SidecarWelcomeMenu({
  session,
  onOpenFileTree,
  onOpenTerminal,
  onOpenBrowser,
  onOpenSideChat,
  onOpenAgentGraph,
  onOpenBlackboard,
}: SidecarWelcomeMenuProps) {
  const menuItems = [
    {
      icon: <IconChalkboard size={18} color="var(--astr-indigo, #6366f1)" />,
      label: "黑板",
      shortcut: "Ctrl+Alt+B",
      onClick: onOpenBlackboard || onOpenFileTree,
    },
    {
      icon: <IconFolder size={18} color="var(--astr-blue, #3b82f6)" />,
      label: '文件',
      shortcut: 'Ctrl+Shift+E',
      onClick: onOpenFileTree,
    },
    {
      icon: <IconMessages size={18} color="var(--astr-indigo, #6366f1)" />,
      label: '侧边聊天',
      shortcut: 'Ctrl+Alt+S',
      onClick: onOpenSideChat,
    },
    {
      icon: <IconBrain size={18} color="var(--astr-purple, #a855f7)" />,
      label: '决策状态机',
      shortcut: 'Ctrl+Alt+G',
      onClick: onOpenAgentGraph,
    },
    {
      icon: <IconWorld size={18} color="var(--astr-cyan, #06b6d4)" />,
      label: '浏览器',
      shortcut: 'Ctrl+T',
      onClick: onOpenBrowser,
    },
    {
      icon: <IconTerminal2 size={18} color="var(--astr-green, #10b981)" />,
      label: '终端',
      shortcut: 'Ctrl+`',
      onClick: onOpenTerminal,
    },
  ]

  return (
    <div
      className="sidecar-welcome-container"
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '100%',
        padding: '32px 24px',
      }}
    >
      <div style={{ width: '100%', maxWidth: '460px' }}>
        {/* Codex 风格功能列表 */}
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '2px',
            background: 'var(--astr-surface)',
            borderRadius: '8px',
            border: '1px solid var(--astr-border)',
            overflow: 'hidden',
          }}
        >
          {menuItems.map((item) => (
            <UnstyledButton
              key={item.label}
              onClick={item.onClick}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '10px 16px',
                transition: 'background 0.12s ease',
              }}
              styles={{
                root: {
                  '&:hover': {
                    background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.04))',
                  },
                },
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  {item.icon}
                </span>
                <Text size="sm" fw={500}>
                  {item.label}
                </Text>
              </div>
              <Text
                size="xs"
                c="dimmed"
                style={{
                  fontFamily: 'monospace',
                  background: 'var(--astr-card)',
                  border: '1px solid var(--astr-border)',
                  padding: '2px 6px',
                  borderRadius: '4px',
                }}
              >
                {item.shortcut}
              </Text>
            </UnstyledButton>
          ))}
        </div>

        {/* 推荐工作区信息 */}
        {session.workspace && (
          <div style={{ marginTop: '24px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '6px', marginBottom: '8px' }}>
              <IconSparkles size={14} color="var(--astr-muted)" />
              <Text size="xs" fw={600} c="dimmed">
                推荐工作区
              </Text>
            </div>
            <UnstyledButton
              onClick={onOpenFileTree}
              style={{
                display: 'block',
                width: '100%',
                padding: '10px 14px',
                borderRadius: '6px',
                background: 'var(--astr-card)',
                border: '1px solid var(--astr-border)',
                cursor: 'pointer',
              }}
            >
              <Text size="xs" fw={500} truncate title={session.workspace}>
                {session.project_name ? `${session.project_name} · ` : ''}
                {session.workspace}
              </Text>
            </UnstyledButton>
          </div>
        )}
      </div>
    </div>
  )
}
