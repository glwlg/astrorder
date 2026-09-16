import { Group, Paper, SimpleGrid, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import {
  IconBolt,
  IconClock,
  IconFolder,
  IconFolderPlus,
} from '@tabler/icons-react'
import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { BrandMark } from '../../components/BrandMark'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { ShinyText } from '../../components/animations/ShinyText'
import type { Agent, Session } from '../../domain/types'

interface WelcomeViewProps {
  sessions: Session[]
  agents: Record<string, Agent>
  onNewSession?: () => void
}

export function WelcomeView({ sessions, agents, onNewSession }: WelcomeViewProps) {
  const navigate = useNavigate()

  const recentSessions = useMemo(() => {
    return [...sessions]
      .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
      .slice(0, 4)
  }, [sessions])

  const shortcuts = [
    { key: 'Ctrl + P', desc: '快速打开文件树' },
    { key: 'Ctrl + Alt + S', desc: '开启侧边副会话' },
    { key: 'Ctrl + `', desc: '打开或切换内置终端' },
    { key: 'Ctrl + T', desc: '唤起原生内置浏览器' },
  ]

  return (
    <div className="welcome-workspace-stage">
      <div className="welcome-inner-container">
        <Stack align="center" gap="xs" mb="xl">
          <div className="welcome-logo-badge">
            <BrandMark size={64} className="welcome-logo-img" />
          </div>
          <Title order={1} size="h3" fw={700} style={{ letterSpacing: '-0.01em', margin: 0 }}>
            <ShinyText text="星序 · 协作工作台" speed={2.5} />
          </Title>
          <Text c="dimmed" size="sm" ta="center" maw={460}>
            群星各有所长，协作自有秩序。从左侧选择已有会话，或开启新的任务。
          </Text>
        </Stack>

        {recentSessions.length > 0 && (
          <div className="welcome-section" style={{ marginBottom: 28 }}>
            <Group justify="space-between" align="center" mb={10}>
              <Text size="xs" fw={600} c="dimmed" style={{ letterSpacing: '0.04em', textTransform: 'uppercase' }}>
                最近活跃会话
              </Text>
              {onNewSession && (
                <UnstyledButton className="welcome-new-btn" onClick={onNewSession}>
                  <Group gap={4}>
                    <IconFolderPlus size={14} />
                    <span>新建会话</span>
                  </Group>
                </UnstyledButton>
              )}
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
              {recentSessions.map((session) => {
                const agent = agents[session.agent_id]
                return (
                  <UnstyledButton
                    key={session.id}
                    className="welcome-card welcome-session-card"
                    onClick={() =>
                      navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
                    }
                  >
                    <Group justify="space-between" align="flex-start" wrap="nowrap" mb={6}>
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <AgentBrandIcon kind={agent?.kind} size={16} />
                        <Text fw={600} size="sm" truncate>
                          {session.title || '未命名会话'}
                        </Text>
                      </Group>
                      {session.status === 'running' && (
                        <Group gap={4} className="welcome-status-running">
                          <IconBolt size={12} />
                          <span style={{ fontSize: 11 }}>运行中</span>
                        </Group>
                      )}
                    </Group>
                    <Group gap={8} c="dimmed" style={{ fontSize: 11 }}>
                      {session.project_name && (
                        <Group gap={4} wrap="nowrap">
                          <IconFolder size={12} />
                          <span style={{ maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {session.project_name}
                          </span>
                        </Group>
                      )}
                      <Group gap={4} wrap="nowrap">
                        <IconClock size={12} />
                        <span>{session.updated_at ? new Date(session.updated_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '刚刚'}</span>
                      </Group>
                    </Group>
                  </UnstyledButton>
                )
              })}
            </SimpleGrid>
          </div>
        )}

        <div className="welcome-section">
          <Text size="xs" fw={600} c="dimmed" mb={10} style={{ letterSpacing: '0.04em', textTransform: 'uppercase' }}>
            工作台快捷操作
          </Text>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            {shortcuts.map((item) => (
              <Paper key={item.key} className="welcome-card welcome-shortcut-card" withBorder={false}>
                <Group justify="space-between" align="center">
                  <Text size="xs" c="dimmed">{item.desc}</Text>
                  <kbd className="welcome-kbd">{item.key}</kbd>
                </Group>
              </Paper>
            ))}
          </SimpleGrid>
        </div>
      </div>
    </div>
  )
}
