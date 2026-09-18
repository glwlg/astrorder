import { useMemo, useState } from 'react'
import {
  ActionIcon,
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
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import type { Agent, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { sessionActivityStatus } from './sessionRailModel'
import { AgentBrandIcon } from './AgentBrandIcon'

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

  // 全量扫描识别所有星系主星与其伴星数量
  const rootStars = useMemo(() => {
    const sessionMap = new Map<string, Session>()
    const childrenMap = new Map<string, string[]>()
    const parentMap = new Map<string, string>()

    for (const s of sessions) {
      const k = scopeKey(s.agent_id, s.id)
      sessionMap.set(k, s)
      childrenMap.set(k, [])
    }

    for (const s of sessions) {
      const childKey = scopeKey(s.agent_id, s.id)
      let parentKey: string | null = null

      if (s.parent_session_key && sessionMap.has(s.parent_session_key)) {
        parentKey = s.parent_session_key
      } else if (s.parent_session_id) {
        const candidateKey = scopeKey(s.parent_agent_id || s.agent_id, s.parent_session_id)
        if (sessionMap.has(candidateKey)) {
          parentKey = candidateKey
        } else {
          for (const [k, p] of sessionMap.entries()) {
            if (p.id === s.parent_session_id) {
              parentKey = k
              break
            }
          }
        }
      }

      if (parentKey && parentKey !== childKey) {
        parentMap.set(childKey, parentKey)
        const list = childrenMap.get(parentKey) || []
        list.push(childKey)
        childrenMap.set(parentKey, list)
      }
    }

    // 筛选所有主星：拥有子会话且自身没有父会话，或者标题明确为群星/主星协同
    const candidateKeys = new Set<string>()
    for (const [pKey, cList] of childrenMap.entries()) {
      if (cList.length > 0 && !parentMap.has(pKey)) {
        candidateKeys.add(pKey)
      }
    }
    for (const s of sessions) {
      const title = s.title || ''
      if ((title.includes('主星') || title.includes('群星') || title.includes('星系')) && !parentMap.has(scopeKey(s.agent_id, s.id))) {
        candidateKeys.add(scopeKey(s.agent_id, s.id))
      }
    }

    // 计算每个主星家族的全部伴星数
    const list = Array.from(candidateKeys).map((k) => {
      const s = sessionMap.get(k)!
      // 深度统计伴星总数
      let workerCount = 0
      const queue = [...(childrenMap.get(k) || [])]
      const visited = new Set<string>()
      while (queue.length > 0) {
        const child = queue.shift()!
        if (visited.has(child)) continue
        visited.add(child)
        workerCount += 1
        const grandchildren = childrenMap.get(child) || []
        for (const gc of grandchildren) queue.push(gc)
      }

      return {
        key: k,
        session: s,
        agent: agents[s.agent_id],
        workerCount,
        status: sessionActivityStatus(s),
      }
    })

    // 按活跃状态与更新时间排序
    list.sort((a, b) => {
      const aActive = a.status === 'running' || a.status === 'waiting_approval' ? 1 : 0
      const bActive = b.status === 'running' || b.status === 'waiting_approval' ? 1 : 0
      if (aActive !== bActive) return bActive - aActive
      return b.session.updated_at.localeCompare(a.session.updated_at)
    })

    return list
  }, [sessions, agents])

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

