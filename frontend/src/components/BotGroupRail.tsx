import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Menu,
  ScrollArea,
  Stack,
  Text,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconDotsVertical,
  IconPlus,
  IconTrash,
  IconUsers,
} from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import type { Agent, BotGroup } from '../domain/types'
import { AgentBrandIcon } from './AgentBrandIcon'
import { CreateBotGroupDialog } from './CreateBotGroupDialog'

export function BotGroupRail({
  agents = {},
  onNavigate,
}: {
  agents?: Record<string, Agent>
  onNavigate?: () => void
}) {
  const navigate = useNavigate()
  const location = useLocation()
  const [botGroups, setBotGroups] = useState<BotGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpened, setCreateOpened] = useState(false)

  const loadGroups = async () => {
    try {
      const res = await api.listBotGroups()
      setBotGroups(res.items || [])
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadGroups()
    const timer = setInterval(() => void loadGroups(), 8000)
    return () => clearInterval(timer)
  }, [])

  const handleDeleteGroup = async (group: BotGroup, e?: React.MouseEvent) => {
    e?.stopPropagation()
    try {
      await api.deleteBotGroup(group.id)
      notifications.show({ color: 'teal', message: `已解散群聊「${group.name}」` })
      setBotGroups((prev) => prev.filter((g) => g.id !== group.id))
      if (location.pathname.includes(group.id)) {
        navigate('/groups')
      }
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '删除群聊失败',
      })
    }
  }

  return (
    <div className="bot-group-rail" style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
      {/* 顶部标题与新建群聊操作区 */}
      <div className="rail-heading" style={{ padding: '0 4px 10px 4px', borderBottom: '1px solid var(--astr-border)' }}>
        <Group justify="space-between" align="center" style={{ width: '100%' }} wrap="nowrap">
          <Group gap={6} wrap="nowrap">
            <IconUsers size={16} color="var(--astr-indigo, #5b6cff)" />
            <Text size="xs" fw={700} c="dimmed">群聊协作 (Bots)</Text>
            <Badge size="xs" variant="light" color="indigo">{botGroups.length}</Badge>
          </Group>
          <Tooltip label="新建多 Agent 群聊" withArrow position="bottom">
            <Button
              size="compact-xs"
              variant="light"
              color="indigo"
              onClick={() => setCreateOpened(true)}
              leftSection={<IconPlus size={13} />}
              style={{ fontWeight: 600 }}
            >
              新建群聊
            </Button>
          </Tooltip>
        </Group>
      </div>

      {/* 群聊会话列表 */}
      <ScrollArea style={{ flex: 1, minHeight: 0, marginTop: 8 }} scrollbarSize={6}>
        {loading && botGroups.length === 0 ? (
          <Text size="xs" c="dimmed" ta="center" p="md">正在加载群聊…</Text>
        ) : botGroups.length === 0 ? (
          <Stack align="center" gap="xs" p="xl" style={{ textAlign: 'center' }}>
            <IconUsers size={28} color="var(--astr-muted)" stroke={1.4} />
            <Text size="xs" c="dimmed">暂无群聊会话</Text>
            <Button size="xs" variant="subtle" color="indigo" onClick={() => setCreateOpened(true)}>
              立即创建
            </Button>
          </Stack>
        ) : (
          <Stack gap={4}>
            {botGroups.map((bg) => {
              const isActive = location.pathname.includes(bg.id)
              return (
                <UnstyledButton
                  key={bg.id}
                  className="session-row"
                  onClick={() => {
                    navigate(`/groups/${bg.id}`)
                    onNavigate?.()
                  }}
                  style={{
                    padding: '8px 10px',
                    borderRadius: 8,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    background: isActive
                      ? 'color-mix(in srgb, var(--astr-indigo, #5b6cff) 14%, var(--astr-surface))'
                      : undefined,
                    border: isActive
                      ? '1px solid color-mix(in srgb, var(--astr-indigo, #5b6cff) 30%, transparent)'
                      : '1px solid transparent',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <Group gap={6} wrap="nowrap" mb={3}>
                      <Text size="sm" fw={isActive ? 650 : 550} truncate style={{ color: 'var(--astr-text)' }}>
                        {bg.name}
                      </Text>
                      <Badge size="xs" variant="outline" color="gray" style={{ flexShrink: 0 }}>
                        {bg.members.length} 人
                      </Badge>
                    </Group>
                    {/* 成员 Agent 缩略图标预览 */}
                    <Group gap={3} wrap="nowrap">
                      {bg.members.slice(0, 5).map((m) => {
                        const ag = agents[m.agent_id]
                        return (
                          <span key={m.agent_id} title={ag?.name || m.name || m.agent_id} style={{ display: 'inline-flex' }}>
                            <AgentBrandIcon kind={ag?.kind} size={13} />
                          </span>
                        )
                      })}
                      {bg.members.length > 5 && (
                        <Text size="xs" c="dimmed">+{bg.members.length - 5}</Text>
                      )}
                    </Group>
                  </div>

                  <Menu position="bottom-end" shadow="md" width={120} withinPortal>
                    <Menu.Target>
                      <ActionIcon
                        variant="subtle"
                        size="xs"
                        color="gray"
                        onClick={(e) => e.stopPropagation()}
                        aria-label="操作"
                      >
                        <IconDotsVertical size={13} />
                      </ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
                      <Menu.Item
                        color="red"
                        leftSection={<IconTrash size={13} />}
                        onClick={(e) => void handleDeleteGroup(bg, e)}
                      >
                        解散群聊
                      </Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </UnstyledButton>
              )
            })}
          </Stack>
        )}
      </ScrollArea>

      <CreateBotGroupDialog
        agents={agents}
        opened={createOpened}
        onClose={() => setCreateOpened(false)}
        onCreated={(g) => {
          setBotGroups((prev) => [g, ...prev])
          navigate(`/groups/${g.id}`)
          onNavigate?.()
        }}
      />
    </div>
  )
}

