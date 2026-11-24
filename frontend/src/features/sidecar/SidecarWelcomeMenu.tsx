import { Text, UnstyledButton } from '@mantine/core'
import {
  BrainIcon, BrowserIcon, ChalkboardSimpleIcon, ChatsCircleIcon, DatabaseIcon, FolderSimpleIcon, GraphIcon, SparkleIcon, TerminalWindowIcon,
} from '../../components/workbenchIcons'
import type { Session } from '../../domain/types'
import { useShortcutStore } from '../shortcuts/shortcuts'

interface SidecarWelcomeMenuProps {
  session: Session
  onOpenFileTree?: () => void
  onOpenTerminal?: () => void
  onOpenBrowser?: () => void
  onOpenDatabase?: () => void
  onOpenSideChat?: () => void
  onOpenAgentGraph?: () => void
  onOpenBlackboard?: () => void
  onOpenMemory?: () => void
}

export function SidecarWelcomeMenu({
  session,
  onOpenFileTree,
  onOpenTerminal,
  onOpenBrowser,
  onOpenDatabase,
  onOpenSideChat,
  onOpenAgentGraph,
  onOpenBlackboard,
  onOpenMemory,
}: SidecarWelcomeMenuProps) {
  const bindings = useShortcutStore(state => state.bindings)
  const menuItems = [
    ...(onOpenBlackboard
      ? [
          {
            icon: <ChalkboardSimpleIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '黑板',
            shortcut: bindings.blackboard || '未设置',
            onClick: onOpenBlackboard,
          },
        ]
      : []),
    ...(onOpenMemory
      ? [
          {
            icon: <BrainIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '知识记忆',
            shortcut: bindings.memory || '未设置',
            onClick: onOpenMemory,
          },
        ]
      : []),
    ...(onOpenFileTree
      ? [
          {
            icon: <FolderSimpleIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '文件',
            shortcut: bindings.fileTree || '未设置',
            onClick: onOpenFileTree,
          },
        ]
      : []),
    ...(onOpenSideChat
      ? [
          {
            icon: <ChatsCircleIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '侧边聊天',
            shortcut: bindings.sideChat || '未设置',
            onClick: onOpenSideChat,
          },
        ]
      : []),
    ...(onOpenAgentGraph
      ? [
          {
            icon: <GraphIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '决策状态机',
            shortcut: bindings.agentGraph || '未设置',
            onClick: onOpenAgentGraph,
          },
        ]
      : []),
    ...(onOpenBrowser
      ? [
          {
            icon: <BrowserIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '浏览器',
            shortcut: bindings.browser || '未设置',
            onClick: onOpenBrowser,
          },
        ]
      : []),
    ...(onOpenTerminal
      ? [
          {
            icon: <TerminalWindowIcon size={20} weight="duotone" color="var(--astr-blue)" />,
            label: '终端',
            shortcut: bindings.terminal || '未设置',
            onClick: onOpenTerminal,
          },
        ]
      : []),
    ...(onOpenDatabase
      ? [
          {
            icon: <DatabaseIcon size={20} weight="duotone" color="#0f766e" />,
            label: '数据库',
            shortcut: '工作台',
            onClick: onOpenDatabase,
          },
        ]
      : []),
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
      <div style={{ width: '100%', maxWidth: '560px' }}>
        <div
          className="sidecar-welcome-list"
          data-liquid-surface="card"
          data-liquid-snapshot=".desktop-sidecar"
        >
          {menuItems.length > 0 ? (
            menuItems.map((item) => (
              <UnstyledButton
                key={item.label}
                onClick={item.onClick}
                className="sidecar-welcome-item"
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
                  className="sidecar-welcome-shortcut"
                >
                  {item.shortcut}
                </Text>
              </UnstyledButton>
            ))
          ) : (
            <div style={{ padding: '18px 16px', textAlign: 'center' }}>
              <Text size="xs" c="dimmed">
                当前未启用侧边工作台工具插件，可在「设置 → 插件」中开启。
              </Text>
            </div>
          )}
        </div>

        {/* 推荐工作区信息 */}
        {session.workspace && onOpenFileTree && (
          <div style={{ marginTop: '20px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '6px', marginBottom: '8px' }}>
              <SparkleIcon size={14} color="var(--astr-indigo)" />
              <Text size="xs" fw={600} c="dimmed">
                推荐工作区
              </Text>
            </div>
            <UnstyledButton
              onClick={onOpenFileTree}
              className="sidecar-workspace-link"
              data-liquid-surface="control"
              data-liquid-snapshot=".desktop-sidecar"
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
