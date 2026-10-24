import { useMemo } from 'react'
import {
  
  Badge,
  Button,
  Group,
  ScrollArea,
  Stack,
  Text,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import {
  IconPlus,
  IconTopologyStarRing,
} from '@tabler/icons-react'
import {  useNavigate, useSearchParams } from 'react-router-dom'
import type { Agent, Session } from '../domain/types'
import { sessionActivityStatus } from './sessionRailModel'
import { AgentBrandIcon } from './AgentBrandIcon'
import { buildSwarmTopology } from '../domain/swarmTopology'

export function SwarmRootRail({
  sessions = [],
  agents = {},
  onNavigate,
}: {
  sessions?: Session[]
  agents?: Record<string, Agent>
  onNavigate?: () => void
}) {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const activeRootKey = searchParams.get('root')

  // 基于统一纯拓扑 helper 识别协作星系主星与其伴星数量
  const topology = useMemo(() => {
    return buildSwarmTopology(sessions)
  }, [sessions])

  const rootStars = useMemo(() => {
    return topology.roots.map((root) => ({
      key: root.key,
      session: root.session,
      agent: agents[root.session.agent_id],
      workerCount: root.workerCount,
      status: sessionActivityStatus(root.session),
    }))
  }, [topology, agents])

  const handleSelectStar = (starKey: string) => {
    navigate(`/swarm?root=${encodeURIComponent(starKey)}`)
    onNavigate?.()
  }

  const handleLaunchNewStar = () => {
    window.dispatchEvent(new CustomEvent('astrorder:open-create-swarm'))
  }

  return (
    <div className="swarm-root-rail" style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
      {/* 顶部标题与启动主星操作 */}
      <div className="rail-heading" style={{ padding: '0 4px 10px 4px', borderBottom: '1px solid var(--astr-border)' }}>
        <Group justify="space-between" align="center" style={{ width: '100%' }} wrap="nowrap">
          <Group gap={6} wrap="nowrap">
            <IconTopologyStarRing size={16} color="var(--astr-indigo, #5b6cff)" />
            <Text size="xs" fw={700} c="dimmed">星图主星列表</Text>
            <Badge size="xs" variant="light" color="indigo">{rootStars.length}</Badge>
          </Group>
          <Tooltip label="启动新主星会话" withArrow position="bottom">
            <Button
              size="compact-xs"
              variant="light"
              color="indigo"
              onClick={handleLaunchNewStar}
              leftSection={<IconPlus size={13} />}
              style={{ fontWeight: 600 }}
            >
              启动主星
            </Button>
          </Tooltip>
        </Group>
      </div>

      {/* 主星列表 */}
      <ScrollArea style={{ flex: 1, minHeight: 0, marginTop: 8 }} scrollbarSize={6}>
        {rootStars.length === 0 ? (
          <Stack align="center" gap="xs" p="xl" style={{ textAlign: 'center' }}>
            <IconTopologyStarRing size={30} color="var(--astr-muted)" stroke={1.4} />
            <Text size="xs" c="dimmed">暂无主星拓扑</Text>
            <Button size="xs" variant="subtle" color="indigo" onClick={handleLaunchNewStar}>
              点亮第一颗主星
            </Button>
          </Stack>
        ) : (
          <Stack gap={4}>
            {rootStars.map((star) => {
              const isSelected = (activeRootKey ? activeRootKey === star.key : rootStars[0]?.key === star.key)
              const isRunning = star.status === 'running' || star.status === 'waiting_approval'

              return (
                <UnstyledButton
                  key={star.key}
                  className="session-row"
                  onClick={() => handleSelectStar(star.key)}
                  style={{
                    padding: '8px 10px',
                    borderRadius: 8,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    background: isSelected
                      ? 'color-mix(in srgb, var(--astr-indigo, #5b6cff) 14%, var(--astr-surface))'
                      : undefined,
                    border: isSelected
                      ? '1px solid color-mix(in srgb, var(--astr-indigo, #5b6cff) 35%, transparent)'
                      : '1px solid transparent',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <Group gap={6} wrap="nowrap" mb={3}>
                      {isRunning && (
                        <span
                          style={{
                            width: 6,
                            height: 6,
                            borderRadius: '50%',
                            background: '#3b82f6',
                            boxShadow: '0 0 0 3px rgba(59, 130, 246, 0.25)',
                            display: 'inline-block',
                            flexShrink: 0,
                          }}
                        />
                      )}
                      <Text size="sm" fw={isSelected ? 650 : 550} truncate style={{ color: 'var(--astr-text)' }}>
                        {star.session.title || '主星协同会话'}
                      </Text>
                    </Group>
                    <Group gap={6} wrap="nowrap">
                      <AgentBrandIcon kind={star.agent?.kind} size={12} />
                      <Text size="xs" c="dimmed">
                        {star.agent?.name || star.session.agent_id}
                      </Text>
                      <Text size="xs" c="dimmed">·</Text>
                      <Badge size="xs" variant="light" color={star.workerCount > 0 ? 'cyan' : 'gray'}>
                        {star.workerCount} 伴星
                      </Badge>
                    </Group>
                  </div>
                </UnstyledButton>
              )
            })}
          </Stack>
        )}
      </ScrollArea>
    </div>
  )
}
