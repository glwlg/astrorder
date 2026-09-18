import { BlackboardItemRenderer } from './BlackboardJsonRender';
import { useEffect, useMemo, useState, useRef, type MouseEvent, type WheelEvent } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Modal,
  SegmentedControl,
  Text,
  Tooltip,
  CloseButton,
  Progress,
  Stack,
  Divider,
  } from '@mantine/core'
import {
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconTrash,
  IconZoomIn,
  IconZoomOut,
  IconAlertTriangle,
  IconTerminal2,
  IconActivity,
  IconExternalLink,
  IconFocusCentered,
  IconArrowsMaximize,
  IconArrowsMinimize,
    IconMaximize,
} from '@tabler/icons-react'
import type { Agent, Session } from '../../domain/types'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { scopeKey } from '../../domain/semantics'
import { MonitorCard } from './MonitorPage'
import { api } from '../../api/client'
import { useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'

export interface SwarmDagNode {
  session: Session
  agent?: Agent
  key: string
  level: number
  x: number
  y: number
  width: number
  height: number
  parentKey?: string | null
  childrenKeys: string[]
}

export interface SwarmDagEdge {
  id: string
  fromKey: string
  toKey: string
  x1: number
  y1: number
  x2: number
  y2: number
  active: boolean
  status?: string
}

// 遵循 Stitch 设计系统的卡片尺寸与层次间距
const CARD_WIDTH = 290
const CARD_HEIGHT = 168
const HORIZONTAL_GAP = 160
const VERTICAL_GAP = 36



function getConstellationSessions(rootNode: SwarmDagNode, allNodes: SwarmDagNode[]): SwarmDagNode[] {
  const result: SwarmDagNode[] = [rootNode]
  const queue: string[] = [...rootNode.childrenKeys]
  const visited = new Set<string>([rootNode.key])
  while (queue.length > 0) {
    const key = queue.shift()!
    if (visited.has(key)) continue
    visited.add(key)
    const child = allNodes.find((n) => n.key === key)
    if (child) {
      result.push(child)
      queue.push(...child.childrenKeys)
    }
  }
  return result
}

export function SwarmDagView({
  allSessions,
  agents,
  activeRootKey,
  onOpenSession,
  onRemoveFromMonitor,
}: {
  allSessions: Session[]
  monitorSessions?: Session[]
  agents: Record<string, Agent>
  activeRootKey?: string | null
  onOpenSession: (session: Session) => void
  onRemoveFromMonitor?: (key: string) => void
}) {
  const queryClient = useQueryClient()
  const [modalSession, setModalSession] = useState<Session | null>(null)
  const [deleteModalData, setDeleteModalData] = useState<{ rootNode: SwarmDagNode; sessions: SwarmDagNode[] } | null>(null)
  const [collapsedRootKeys, setCollapsedRootKeys] = useState<Set<string>>(new Set())

  const toggleCollapse = (rootKey: string) => {
    setCollapsedRootKeys((prev) => {
      const next = new Set(prev)
      if (next.has(rootKey)) {
        next.delete(rootKey)
      } else {
        next.add(rootKey)
      }
      return next
    })
  }
  const [isDeleting, setIsDeleting] = useState(false)
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [isWideInspector, setIsWideInspector] = useState(false)
  const [blackboardModalOpen, setBlackboardModalOpen] = useState(false)
  const [telemetries, setTelemetries] = useState<Record<string, { progress?: number; phase?: string; summary?: string; status?: string }>>({})
  const [blackboardScope, setBlackboardScope] = useState<'swarm' | 'global'>('swarm')
  const [swarmBlackboard, setSwarmBlackboard] = useState<Record<string, unknown>>({})
  const [globalBlackboard, setGlobalBlackboard] = useState<Record<string, unknown>>({})
  const [activeSosKeys, setActiveSosKeys] = useState<Set<string>>(new Set())
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 60, y: 70 })
  const isDraggingRef = useRef(false)
  const isMovedRef = useRef(false)
  const lastMouseRef = useRef({ x: 0, y: 0 })

  useEffect(() => {
    setPan({ x: 60, y: 70 })
    setZoom(1)
  }, [activeRootKey])

  useEffect(() => {
    const fetchTelemetry = async () => {
      try {
        const res = await api.getTelemetry()
        if (res.items) setTelemetries(res.items)
      } catch {}
    }
    void fetchTelemetry()

    const onTelEvent = (e: any) => {
      if (e.detail?.key && e.detail?.progress !== undefined) {
        setTelemetries((prev) => ({ ...prev, [e.detail.key]: e.detail }))
      }
    }
    window.addEventListener('astrorder:swarm-telemetry-changed', onTelEvent)
    const onSosEvent = () => {
      void api.listSos().then((res) => {
        if (res.items) {
          setActiveSosKeys(new Set(res.items.map((s: any) => s.worker_key)))
        }
      }).catch(() => {})
    }
    window.addEventListener('astrorder:swarm-sos-changed', onSosEvent)
    onSosEvent()
    return () => {
      window.removeEventListener('astrorder:swarm-telemetry-changed', onTelEvent)
      window.removeEventListener('astrorder:swarm-sos-changed', onSosEvent)
    }
  }, [])



  // 构建星图拓扑结构
  const { nodes, edges, rootCount, workerCount } = useMemo(() => {
    const sessionMap = new Map<string, Session>()
    const childrenMap = new Map<string, string[]>()
    const parentMap = new Map<string, string>()

    for (const s of allSessions) {
      const k = scopeKey(s.agent_id, s.id)
      sessionMap.set(k, s)
      childrenMap.set(k, [])
    }

    // 全量扫描识别父子血缘映射
    for (const s of allSessions) {
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

    const constellationKeys = new Set<string>()
    for (const [pKey, cList] of childrenMap.entries()) {
      if (cList.length > 0) {
        constellationKeys.add(pKey)
        cList.forEach((c) => constellationKeys.add(c))
      }
    }
    for (const cKey of parentMap.keys()) {
      constellationKeys.add(cKey)
    }

    // 拓扑分层 (Rank assignment)
    const levelMap = new Map<string, number>()
    const computeLevel = (k: string, visited = new Set<string>()): number => {
      if (levelMap.has(k)) return levelMap.get(k)!
      if (visited.has(k)) return 0
      visited.add(k)
      const pKey = parentMap.get(k)
      if (!pKey || !constellationKeys.has(pKey)) {
        levelMap.set(k, 0)
        return 0
      }
      const lvl = computeLevel(pKey, visited) + 1
      levelMap.set(k, lvl)
      return lvl
    }

    for (const k of constellationKeys) {
      computeLevel(k)
    }

    const layers = new Map<number, string[]>()
    let maxLevel = 0
    for (const [k, lvl] of levelMap.entries()) {
      if (!layers.has(lvl)) layers.set(lvl, [])
      layers.get(lvl)!.push(k)
      if (lvl > maxLevel) maxLevel = lvl
    }

    const COLLAPSED_HEIGHT = 46
    const calculatedNodes: SwarmDagNode[] = []
    const nodeCoords = new Map<string, { x: number; y: number; width: number; height: number }>()

    // 按各个星系主星独立聚合排布，支持单星系收起与垂直空间自适应
    const rootKeys = Array.from(constellationKeys).filter((k) => !parentMap.has(k))
    let currentY = 0
    const CONSTELLATION_GAP = 28

    // 一次只展示一个选中的主星拓扑
    const selectedTargetKey = activeRootKey && rootKeys.includes(activeRootKey)
      ? activeRootKey
      : (rootKeys.length > 0 ? rootKeys[0] : null)
    const targetRoots = selectedTargetKey ? [selectedTargetKey] : []

    for (const rootKey of targetRoots) {
      const isCollapsed = collapsedRootKeys.has(rootKey)
      const rootSession = sessionMap.get(rootKey)
      if (!rootSession) continue

      // 收集该星系所有节点并层级分级
      const familyNodes: { key: string; level: number }[] = []
      const queue: { key: string; level: number }[] = [{ key: rootKey, level: 0 }]
      const visited = new Set<string>()

      while (queue.length > 0) {
        const item = queue.shift()!
        if (visited.has(item.key)) continue
        visited.add(item.key)
        familyNodes.push(item)
        const children = childrenMap.get(item.key) || []
        for (const childKey of children) {
          queue.push({ key: childKey, level: item.level + 1 })
        }
      }

      if (isCollapsed) {
        // 折叠态：主星收缩为 46px 紧凑胶囊，伴星隐藏
        nodeCoords.set(rootKey, { x: 0, y: currentY, width: CARD_WIDTH, height: COLLAPSED_HEIGHT })
        calculatedNodes.push({
          session: rootSession,
          agent: agents[rootSession.agent_id],
          key: rootKey,
          level: 0,
          x: 0,
          y: currentY,
          width: CARD_WIDTH,
          height: COLLAPSED_HEIGHT,
          parentKey: null,
          childrenKeys: childrenMap.get(rootKey) || [],
        })
        currentY += COLLAPSED_HEIGHT + CONSTELLATION_GAP
      } else {
        // 展开态：排布各层级伴星
        const familyLayers = new Map<number, string[]>()
        for (const item of familyNodes) {
          if (!familyLayers.has(item.level)) familyLayers.set(item.level, [])
          familyLayers.get(item.level)!.push(item.key)
        }

        let maxLayerCount = 1
        for (const list of familyLayers.values()) {
          if (list.length > maxLayerCount) maxLayerCount = list.length
        }
        const constellationHeight = maxLayerCount * (CARD_HEIGHT + VERTICAL_GAP) - VERTICAL_GAP

        for (const [lvl, keys] of familyLayers.entries()) {
          const startX = lvl * (CARD_WIDTH + HORIZONTAL_GAP)
          keys.forEach((k, idx) => {
            const s = sessionMap.get(k)!
            const y = currentY + idx * (CARD_HEIGHT + VERTICAL_GAP)
            nodeCoords.set(k, { x: startX, y, width: CARD_WIDTH, height: CARD_HEIGHT })
            calculatedNodes.push({
              session: s,
              agent: agents[s.agent_id],
              key: k,
              level: lvl,
              x: startX,
              y,
              width: CARD_WIDTH,
              height: CARD_HEIGHT,
              parentKey: parentMap.get(k) || null,
              childrenKeys: childrenMap.get(k) || [],
            })
          })
        }
        currentY += constellationHeight + CONSTELLATION_GAP
      }
    }

    // 生成平滑连接边
    const calculatedEdges: SwarmDagEdge[] = []
    for (const [childKey, parentKey] of parentMap.entries()) {
      if (!constellationKeys.has(childKey) || !constellationKeys.has(parentKey)) continue
      const fromPos = nodeCoords.get(parentKey)
      const toPos = nodeCoords.get(childKey)
      if (fromPos && toPos) {
        const parentSession = sessionMap.get(parentKey)
        const childSession = sessionMap.get(childKey)
        const isActive =
          parentSession?.status === 'running' ||
          parentSession?.status === 'waiting_approval' ||
          childSession?.status === 'running' ||
          childSession?.status === 'waiting_approval'
        calculatedEdges.push({
          id: `${parentKey}->${childKey}`,
          fromKey: parentKey,
          toKey: childKey,
          x1: fromPos.x + fromPos.width,
          y1: fromPos.y + fromPos.height / 2,
          x2: toPos.x,
          y2: toPos.y + toPos.height / 2,
          active: Boolean(isActive),
          status: childSession?.status || parentSession?.status,
        })
      }
    }

    const roots = calculatedNodes.filter((n) => n.level === 0).length
    const workers = calculatedNodes.length - roots

    return {
      nodes: calculatedNodes,
      edges: calculatedEdges,
      rootCount: roots,
      workerCount: workers,
    }
  }, [allSessions, agents, collapsedRootKeys])

  // 默认选中第一个主星或伴星
  useEffect(() => {
    if (!selectedKey && nodes.length > 0) {
      setSelectedKey(nodes[0].key)
    }
  }, [nodes[0]?.key, selectedKey])

  const selectedNode = useMemo(() => {
    return nodes.find((n) => n.key === selectedKey) || null
  }, [nodes, selectedKey])

  // 计算选中节点所属星系的主星根 Key，实现星系黑板物理隔离
  const constellationRootKey = useMemo(() => {
    if (!selectedNode) return null
    let curr: SwarmDagNode | undefined = selectedNode
    const visited = new Set<string>()
    while (curr && curr.parentKey && !visited.has(curr.key)) {
      visited.add(curr.key)
      const parent = nodes.find((n) => n.key === curr!.parentKey)
      if (parent) {
        curr = parent
      } else {
        break
      }
    }
    return curr?.key || selectedNode.key
  }, [selectedNode, nodes])

  // 随选中星系动态隔离加载当前星系黑板与全局黑板
  useEffect(() => {
    const fetchSwarmBlackboard = async () => {
      if (!constellationRootKey) {
        setSwarmBlackboard({})
        return
      }
      try {
        const res = await api.getBlackboard(`swarm:${constellationRootKey}`)
        setSwarmBlackboard(res.items || {})
      } catch {
        setSwarmBlackboard({})
      }
    }
    const fetchGlobalBlackboard = async () => {
      try {
        const res = await api.getBlackboard('global')
        setGlobalBlackboard(res.items || {})
      } catch {
        setGlobalBlackboard({})
      }
    }
    void fetchSwarmBlackboard()
    void fetchGlobalBlackboard()
  }, [constellationRootKey])

  const handleMouseDown = (e: MouseEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    isDraggingRef.current = true
    isMovedRef.current = false
    lastMouseRef.current = { x: e.clientX, y: e.clientY }
  }

  const handleMouseMove = (e: MouseEvent<HTMLDivElement>) => {
    if (!isDraggingRef.current) return
    const dx = e.clientX - lastMouseRef.current.x
    const dy = e.clientY - lastMouseRef.current.y
    if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
      isMovedRef.current = true
    }
    lastMouseRef.current = { x: e.clientX, y: e.clientY }
    setPan((p) => ({ x: p.x + dx, y: p.y + dy }))
  }

  const handleMouseUp = () => {
    isDraggingRef.current = false
  }

  const handleWheel = (e: WheelEvent<HTMLDivElement>) => {
    e.preventDefault()
    const zoomDelta = e.deltaY < 0 ? 0.08 : -0.08
    setZoom((z) => Math.min(Math.max(z + zoomDelta, 0.4), 1.8))
  }

  // 状态统计
  const runningCount = nodes.filter((n) => n.session.status === 'running').length
  const waitingCount = nodes.filter((n) => n.session.status === 'waiting_approval').length
  const sosCount = nodes.filter((n) => activeSosKeys.has(n.key)).length
  const idleCount = nodes.length - runningCount - waitingCount - sosCount

  return (
    <div
      className="swarm-dag-viewport dot-grid"
      style={{
        position: 'relative',
        width: '100%',
        height: 'calc(100vh - 160px)',
        minHeight: 580,
        overflow: 'hidden',
        borderRadius: 12,
        background: 'var(--astr-canvas-bg, #f8f9fb)',
        border: '1px solid var(--astr-border, #e5e7eb)',
        cursor: isDraggingRef.current ? 'grabbing' : 'default',
        userSelect: 'none',
      }}
      onMouseDown={handleMouseDown}
      onMouseMove={handleMouseMove}
      onMouseUp={handleMouseUp}
      onMouseLeave={handleMouseUp}
      onWheel={handleWheel}
    >
      {/* 顶部左侧：坐标指示与视口控制 HUD */}
      <div
        style={{
          position: 'absolute',
          top: 14,
          left: 16,
          zIndex: 15,
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          padding: '6px 12px',
          borderRadius: 8,
          background: 'var(--astr-surface, #ffffff)',
          border: '1px solid var(--astr-border, #e5e7eb)',
          boxShadow: '0 1px 4px rgba(0, 0, 0, 0.04)',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onWheel={(e) => e.stopPropagation()}
      >
        <Group gap="xs">
          <Badge
            variant="light"
            color="indigo"
            size="sm"
            style={{ fontWeight: 600, letterSpacing: '0.2px' }}
          >
            星图阵列
          </Badge>
          <Text size="xs" fw={500} c="dimmed">
            {rootCount} 个主星 · {workerCount} 个伴星
          </Text>
        </Group>
        <div style={{ width: 1, height: 16, background: 'var(--astr-border, #e5e7eb)' }} />
        <Group gap={3}>
          <Tooltip label="缩小" withArrow>
            <Button
              variant="subtle"
              color="gray"
              size="compact-xs"
              onClick={() => setZoom((z) => Math.max(z - 0.15, 0.4))}
            >
              <IconZoomOut size={14} />
            </Button>
          </Tooltip>
          <Text size="xs" fw={600} style={{ minWidth: 38, textAlign: 'center' }} c="dimmed">
            {Math.round(zoom * 100)}%
          </Text>
          <Tooltip label="放大" withArrow>
            <Button
              variant="subtle"
              color="gray"
              size="compact-xs"
              onClick={() => setZoom((z) => Math.min(z + 0.15, 1.8))}
            >
              <IconZoomIn size={14} />
            </Button>
          </Tooltip>
          <Tooltip label="居中复位" withArrow>
            <Button
              variant="subtle"
              color="gray"
              size="compact-xs"
              onClick={() => {
                setZoom(1)
                setPan({ x: 60, y: 70 })
              }}
            >
              <IconFocusCentered size={14} />
            </Button>
          </Tooltip>
        </Group>
      </div>

      {nodes.length === 0 && (
        <div
          style={{
            position: 'absolute',
            top: '50%',
            left: '50%',
            transform: 'translate(-50%, -50%)',
            textAlign: 'center',
            pointerEvents: 'none',
          }}
        >
          <div
            style={{
              width: 64,
              height: 64,
              borderRadius: 16,
              margin: '0 auto 12px',
              border: '1px solid var(--astr-border, #e5e7eb)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              background: 'var(--astr-surface, #ffffff)',
              boxShadow: '0 2px 8px rgba(0,0,0,0.04)',
            }}
          >
            <IconActivity size={28} color="var(--astr-indigo, #5b5bd6)" />
          </div>
          <Text size="sm" fw={600} c="var(--astr-text, #202124)">
            暂无派生伴星的星系
          </Text>
          <Text size="xs" c="dimmed" mt={4} style={{ maxWidth: 360, lineHeight: 1.6 }}>
            可通过主星对话输入 <span style={{ color: 'var(--astr-indigo, #5b5bd6)', fontWeight: 600 }}>@群星</span> 派生伴星协同任务，拓扑链路将在此动态延展。
          </Text>
        </div>
      )}

      {/* SVG 战术连接线与导管图层 */}
      <div
        style={{
          transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
          transformOrigin: '0 0',
          position: 'absolute',
          top: 0,
          left: 0,
          width: 4000,
          height: 4000,
          pointerEvents: 'none',
        }}
      >
        <svg
          style={{
            width: '100%',
            height: '100%',
            overflow: 'visible',
            position: 'absolute',
            top: 0,
            left: 0,
          }}
        >
          <defs>
            <marker
              id="arrow-default"
              markerWidth="10"
              markerHeight="8"
              refX="9"
              refY="4"
              orient="auto"
            >
              <polygon points="0 0.5, 9 4, 0 7.5" fill="var(--astr-border, #cbd5e1)" />
            </marker>
            <marker
              id="arrow-blue"
              markerWidth="11"
              markerHeight="8"
              refX="10"
              refY="4"
              orient="auto"
            >
              <polygon points="0 0.5, 10 4, 0 7.5" fill="#5B5BD6" />
            </marker>
            <marker
              id="arrow-amber"
              markerWidth="11"
              markerHeight="8"
              refX="10"
              refY="4"
              orient="auto"
            >
              <polygon points="0 0.5, 10 4, 0 7.5" fill="#D97706" />
            </marker>
          </defs>

          {/* 拓扑连接线 */}
          {edges.map((e) => {
            const midX = (e.x1 + e.x2) / 2
            const d = `M ${e.x1} ${e.y1} C ${midX} ${e.y1}, ${midX} ${e.y2}, ${e.x2} ${e.y2}`
            const isWaiting = e.status === 'waiting_approval'
            const strokeColor = isWaiting ? '#D97706' : '#5B5BD6'
            const markerId = isWaiting ? 'url(#arrow-amber)' : 'url(#arrow-blue)'

            return (
              <g key={e.id}>
                {/* 基础静止导轨 */}
                <path
                  d={d}
                  fill="none"
                  stroke="var(--astr-border, #E5E7EB)"
                  strokeWidth={2}
                  markerEnd={e.active ? undefined : 'url(#arrow-default)'}
                />
                {/* 活跃动态脉冲导管 */}
                {e.active && (
                  <path
                    className="conduit-flow"
                    d={d}
                    fill="none"
                    stroke={strokeColor}
                    strokeWidth={2}
                    markerEnd={markerId}
                  />
                )}
              </g>
            )
          })}
        </svg>

        {/* 开发者卡片节点 DOM 图层 */}
        {nodes.map((node, idx) => {
          const isRunning = node.session.status === 'running'
          const isWaiting = node.session.status === 'waiting_approval'
          const hasSos = activeSosKeys.has(node.key)
          const isRoot = node.level === 0
          const isSelected = node.key === selectedKey
          const tel = telemetries[node.key]

          return (
            <div
              key={node.key}
              style={{
                position: 'absolute',
                left: node.x,
                top: node.y,
                width: node.width,
                pointerEvents: 'auto',
                cursor: 'pointer',
              }}
              onClick={() => {
                if (isMovedRef.current) return
                setSelectedKey(node.key)
              }}
              onDoubleClick={() => {
                setModalSession(node.session)
              }}
            >
              <div
                className="swarm-node-card"
                style={{
                  background: 'var(--astr-surface, #ffffff)',
                  borderRadius: 10,
                  border: isSelected
                    ? '2px solid var(--astr-indigo, #5B5BD6)'
                    : isWaiting
                    ? '1.5px solid #F59E0B'
                    : hasSos
                    ? '1.5px solid #EF4444'
                    : isRunning
                    ? '1.5px solid #10B981'
                    : '1px solid var(--astr-border, #E5E7EB)',
                  boxShadow: isSelected
                    ? '0 4px 16px rgba(91, 91, 214, 0.16)'
                    : '0 1px 3px rgba(0, 0, 0, 0.05)',
                  transition: 'border-color 0.15s ease, box-shadow 0.15s ease, transform 0.15s ease',
                  overflow: 'hidden',
                }}
              >
                {/* 折叠收起态紧凑主星胶囊 */}
                {isRoot && collapsedRootKeys.has(node.key) ? (
                  <div
                    style={{
                      padding: '8px 12px',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      height: 46,
                      boxSizing: 'border-box',
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                      <AgentBrandIcon kind={node.agent?.kind} size={18} />
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
                        <span
                          style={{
                            fontSize: 11,
                            fontWeight: 700,
                            fontFamily: 'monospace',
                            color: '#5B5BD6',
                            background: 'rgba(91, 91, 214, 0.1)',
                            padding: '1px 6px',
                            borderRadius: 4,
                          }}
                        >
                          🌟 主星
                        </span>
                        <span
                          style={{
                            fontWeight: 600,
                            fontSize: 13,
                            color: 'var(--astr-text, #202124)',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            maxWidth: 95,
                          }}
                          title={node.session.title || '主星会话'}
                        >
                          {node.session.title || '主星会话'}
                        </span>
                        {node.childrenKeys.length > 0 && (
                          <Badge size="xs" variant="light" color="gray">
                            已收起 {node.childrenKeys.length} 伴星
                          </Badge>
                        )}
                      </div>
                    </div>

                    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                      <span
                        style={{
                          width: 7,
                          height: 7,
                          borderRadius: '50%',
                          background: hasSos ? '#EF4444' : isRunning ? '#10B981' : isWaiting ? '#F59E0B' : '#94A3B8',
                        }}
                      />
                      <Tooltip label="展开星系与伴星" withArrow>
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="indigo"
                          onClick={(e) => {
                            e.stopPropagation()
                            toggleCollapse(node.key)
                          }}
                        >
                          <IconChevronDown size={14} />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label="删除整个星系" withArrow>
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="red"
                          onClick={(e) => {
                            e.stopPropagation()
                            const chain = getConstellationSessions(node, nodes)
                            setDeleteModalData({ rootNode: node, sessions: chain })
                          }}
                        >
                          <IconTrash size={13} />
                        </ActionIcon>
                      </Tooltip>
                    </div>
                  </div>
                ) : (
                  <>
                {/* 节点头部 */}
                <div
                  style={{
                    padding: '8px 12px',
                    borderBottom: '1px solid var(--astr-border, #F1F5F9)',
                    background: isSelected
                      ? 'color-mix(in srgb, var(--astr-indigo, #5B5BD6) 5%, transparent)'
                      : 'var(--astr-surface-muted, #F8FAFC)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span
                      style={{
                        fontSize: 11,
                        fontWeight: 600,
                        fontFamily: 'monospace',
                        color: isRoot ? '#5B5BD6' : 'var(--astr-text, #475569)',
                        background: isRoot ? 'rgba(91, 91, 214, 0.1)' : 'rgba(148, 163, 184, 0.15)',
                        padding: '1px 6px',
                        borderRadius: 4,
                      }}
                    >
                      {isRoot ? '🌟 主星 Alpha' : `🪐 伴星 L${node.level}`}
                    </span>
                    <span style={{ fontSize: 11, color: '#94A3B8', fontFamily: 'monospace' }}>
                      #{String(idx).padStart(2, '0')}
                    </span>
                  </div>

                  {/* 状态徽标与主星删除按钮 */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    {isRoot && (
                      <>
                        <Tooltip label="删除整个星系（级联删除伴星与黑板）" withArrow>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="red"
                            onClick={(e) => {
                              e.stopPropagation()
                              const chain = getConstellationSessions(node, nodes)
                              setDeleteModalData({ rootNode: node, sessions: chain })
                            }}
                          >
                            <IconTrash size={13} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="收起星系伴星与卡片详情" withArrow>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            onClick={(e) => {
                              e.stopPropagation()
                              toggleCollapse(node.key)
                            }}
                          >
                            <IconChevronUp size={14} />
                          </ActionIcon>
                        </Tooltip>
                      </>
                    )}
                    {hasSos ? (
                      <span
                        style={{
                          fontSize: 11,
                          fontWeight: 600,
                          color: '#B91C1C',
                          background: '#FEF2F2',
                          border: '1px solid #FECACA',
                          padding: '1px 7px',
                          borderRadius: 99,
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 4,
                        }}
                      >
                        <IconAlertTriangle size={12} color="#DC2626" />
                        紧急求援
                      </span>
                    ) : isWaiting ? (
                      <span
                        style={{
                          fontSize: 11,
                          fontWeight: 600,
                          color: '#B45309',
                          background: '#FFFBEB',
                          border: '1px solid #FDE68A',
                          padding: '1px 7px',
                          borderRadius: 99,
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 4,
                        }}
                      >
                        <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#F59E0B' }} />
                        等待审批
                      </span>
                    ) : isRunning ? (
                      <span
                        style={{
                          fontSize: 11,
                          fontWeight: 600,
                          color: '#047857',
                          background: '#ECFDF5',
                          border: '1px solid #A7F3D0',
                          padding: '1px 7px',
                          borderRadius: 99,
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 4,
                        }}
                      >
                        <span
                          className="pulsing-emerald-dot"
                          style={{ width: 6, height: 6, borderRadius: '50%', background: '#10B981' }}
                        />
                        运行中
                      </span>
                    ) : (
                      <span
                        style={{
                          fontSize: 11,
                          color: '#64748B',
                          background: '#F1F5F9',
                          padding: '1px 7px',
                          borderRadius: 99,
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 4,
                        }}
                      >
                        <IconCheck size={12} color="#64748B" />
                        就绪
                      </span>
                    )}
                  </div>
                </div>

                {/* 节点主体 */}
                <div style={{ padding: '10px 12px' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                    <AgentBrandIcon kind={node.agent?.kind} size={18} />
                    <div
                      style={{
                        fontWeight: 600,
                        fontSize: 13,
                        color: 'var(--astr-text, #202124)',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        flex: 1,
                      }}
                      title={node.session.title || '未命名会话'}
                    >
                      {node.session.title || '未命名会话'}
                    </div>
                  </div>

                  <Text size="xs" c="dimmed" lineClamp={1} style={{ fontSize: 11 }}>
                    {node.agent?.name || 'Agent'} · {node.session.workspace || '工作区'}
                  </Text>

                  {/* 主星规格 Bento 矩阵 */}
                  {isRoot ? (
                    <div
                      style={{
                        display: 'grid',
                        gridTemplateColumns: 'repeat(3, 1fr)',
                        gap: 6,
                        padding: '6px 8px',
                        marginTop: 8,
                        borderRadius: 6,
                        background: 'var(--astr-surface-muted, #F8FAFC)',
                        border: '1px solid var(--astr-border, #E5E7EB)',
                        fontSize: 11,
                        textAlign: 'center',
                      }}
                    >
                      <div>
                        <div style={{ color: '#94A3B8', fontSize: 10 }}>直连层级</div>
                        <div style={{ fontWeight: 600, fontFamily: 'monospace' }}>L0 顶层</div>
                      </div>
                      <div>
                        <div style={{ color: '#94A3B8', fontSize: 10 }}>伴星数量</div>
                        <div style={{ fontWeight: 600, fontFamily: 'monospace', color: '#5B5BD6' }}>
                          {node.childrenKeys.length} 节点
                        </div>
                      </div>
                      <div>
                        <div style={{ color: '#94A3B8', fontSize: 10 }}>会话模式</div>
                        <div style={{ fontWeight: 600, fontFamily: 'monospace' }}>主控调度</div>
                      </div>
                    </div>
                  ) : (
                    /* 伴星遥测进度条 */
                    <div style={{ marginTop: 8 }}>
                      <div
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          fontSize: 11,
                          marginBottom: 4,
                        }}
                      >
                        <span style={{ color: '#94A3B8' }}>执行进度</span>
                        <span style={{ fontWeight: 600, fontFamily: 'monospace', color: '#5B5BD6' }}>
                          {tel?.progress !== undefined ? `${tel.progress}%` : isRunning ? '执行中…' : '100%'}
                        </span>
                      </div>
                      <Progress
                        value={tel?.progress !== undefined ? tel.progress : isRunning ? 60 : 100}
                        size="xs"
                        color={isWaiting ? 'yellow' : isRunning ? 'indigo' : 'gray'}
                        radius="xl"
                      />
                      {tel?.summary && (
                        <div
                          style={{
                            fontSize: 10.5,
                            color: 'var(--astr-text, #475569)',
                            background: 'var(--astr-surface-muted, #F8FAFC)',
                            padding: '3px 6px',
                            borderRadius: 4,
                            marginTop: 5,
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                        >
                          {tel.summary}
                        </div>
                      )}
                    </div>
                  )}

                  {/* 待审批快捷提示条 */}
                  {isWaiting && (
                    <div
                      style={{
                        marginTop: 8,
                        padding: '4px 8px',
                        background: '#FFFBEB',
                        border: '1px solid #FDE68A',
                        borderRadius: 6,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        fontSize: 11,
                      }}
                    >
                      <span style={{ color: '#92400E', fontWeight: 500 }}>需要指令授权</span>
                      <Button
                        size="compact-xs"
                        variant="light"
                        color="yellow"
                        onClick={(e) => {
                          e.stopPropagation()
                          setModalSession(node.session)
                        }}
                      >
                        快速核准
                      </Button>
                    </div>
                  )}
                </div>
                  </>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {/* 4. 浮动节点检视舱 (Floating Node Inspector, 默认 460px 宽度，可自由加宽至 680px) */}
      {selectedNode && (
        <aside
          style={{
            position: 'absolute',
            right: 16,
            top: 14,
            bottom: 14,
            width: isWideInspector ? 680 : 460,
            background: 'var(--astr-surface, #ffffff)',
            borderRadius: 12,
            border: '1px solid var(--astr-border, #E5E7EB)',
            boxShadow: '0 8px 28px rgba(0, 0, 0, 0.12)',
            zIndex: 20,
            display: 'flex',
            flexDirection: 'column',
            overflow: 'hidden',
            transition: 'width 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
          }}
          onMouseDown={(e) => e.stopPropagation()}
          onWheel={(e) => e.stopPropagation()}
        >
          {/* 检视舱头部 */}
          <div
            style={{
              padding: '12px 14px',
              borderBottom: '1px solid var(--astr-border, #F1F5F9)',
              background: 'var(--astr-surface-muted, #F8FAFC)',
              display: 'flex',
              alignItems: 'flex-start',
              justifyContent: 'space-between',
            }}
          >
            <div>
              <div style={{ fontSize: 11, color: '#94A3B8', fontFamily: 'monospace', marginBottom: 2 }}>
                {selectedNode.level === 0 ? '🌟 主星 (Alpha 核心)' : `🌟 主星 → 🪐 伴星 L${selectedNode.level}`}
              </div>
              <div style={{ fontWeight: 700, fontSize: 14, color: 'var(--astr-text, #202124)' }}>
                {selectedNode.session.title || '会话详情'}
              </div>
            </div>
            <Group gap={6}>
              <Tooltip label={isWideInspector ? '恢复标准宽度' : '加宽检视舱'} withArrow>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="sm"
                  onClick={() => setIsWideInspector((w) => !w)}
                >
                  {isWideInspector ? <IconArrowsMinimize size={15} /> : <IconArrowsMaximize size={15} />}
                </ActionIcon>
              </Tooltip>
              <CloseButton size="sm" onClick={() => setSelectedKey(null)} />
            </Group>
          </div>

          {/* 检视舱内容区（阻断冒泡，自由上下滚动） */}
          <div
            style={{
              flex: 1,
              overflowY: 'auto',
              padding: 14,
            }}
            className="space-y-4"
          >
            {/* 执行态遥测 */}
            <div>
              <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
                执行态遥测
              </Text>
              <div
                style={{
                  marginTop: 8,
                  padding: 10,
                  borderRadius: 8,
                  background: 'var(--astr-surface-muted, #F8FAFC)',
                  border: '1px solid var(--astr-border, #E5E7EB)',
                }}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, marginBottom: 4 }}>
                  <span style={{ color: '#64748B' }}>执行阶段</span>
                  <span style={{ fontWeight: 600, color: 'var(--astr-text, #202124)' }}>
                    {telemetries[selectedNode.key]?.phase || (selectedNode.session.status === 'running' ? '对局协作执行中' : '就绪待命')}
                  </span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, marginBottom: 6 }}>
                  <span style={{ color: '#64748B' }}>总进度</span>
                  <span style={{ fontWeight: 600, fontFamily: 'monospace', color: '#5B5BD6' }}>
                    {telemetries[selectedNode.key]?.progress !== undefined
                      ? `${telemetries[selectedNode.key].progress}%`
                      : selectedNode.session.status === 'running' ? '65%' : '100%'}
                  </span>
                </div>
                <Progress
                  value={typeof telemetries[selectedNode.key]?.progress === 'number'
                    ? telemetries[selectedNode.key].progress!
                    : selectedNode.session.status === 'running' ? 65 : 100}
                  size="sm"
                  color="indigo"
                  radius="xl"
                />
                {telemetries[selectedNode.key]?.summary && (
                  <Text size="xs" c="dimmed" mt={8}>
                    {telemetries[selectedNode.key].summary}
                  </Text>
                )}
              </div>
            </div>

            <Divider />

            {/* 共享黑板与协作状态（星系命名空间隔离） */}
            <div>
              <Group justify="space-between" mb={8}>
                <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
                  作战黑板 (BLACKBOARD)
                </Text>
                <Group gap={6}>
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="indigo"
                    leftSection={<IconMaximize size={12} />}
                    onClick={() => setBlackboardModalOpen(true)}
                  >
                    全屏大板
                  </Button>
                </Group>
              </Group>

              {/* 命名空间切换：星系专属 vs 全局广播 */}
              <SegmentedControl
                size="xs"
                fullWidth
                value={blackboardScope}
                onChange={(val: string) => setBlackboardScope(val as 'swarm' | 'global')}
                data={[
                  { label: `🪐 星系黑板 (${Object.keys(swarmBlackboard).length})`, value: 'swarm' },
                  { label: `🌐 全局共享 (${Object.keys(globalBlackboard).length})`, value: 'global' },
                ]}
                mb={10}
              />

              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                {Object.keys(blackboardScope === 'swarm' ? swarmBlackboard : globalBlackboard).length > 0 ? (
                  Object.entries(blackboardScope === 'swarm' ? swarmBlackboard : globalBlackboard).slice().reverse().map(([k, v]) => (
                    <BlackboardItemRenderer
                      key={k}
                      itemKey={k}
                      itemValue={v}
                      namespace={blackboardScope === 'swarm' && constellationRootKey ? `swarm:${constellationRootKey}` : 'global'}
                      onUpdated={() => {
                        if (blackboardScope === 'swarm' && constellationRootKey) {
                          void api.getBlackboard(`swarm:${constellationRootKey}`).then((res) => setSwarmBlackboard(res.items || {}))
                        } else {
                          void api.getBlackboard('global').then((res) => setGlobalBlackboard(res.items || {}))
                        }
                      }}
                    />
                  ))
                ) : (
                  <div
                    style={{
                      padding: 16,
                      textAlign: 'center',
                      fontSize: 12,
                      color: '#94A3B8',
                      background: 'var(--astr-surface-muted, #F8FAFC)',
                      borderRadius: 8,
                      border: '1px dashed var(--astr-border, #E5E7EB)',
                    }}
                  >
                    {blackboardScope === 'swarm' ? '当前星系暂无专属黑板变量' : '全局空间暂无共享黑板变量'}
                  </div>
                )}
              </div>
            </div>

            <Divider />

            {/* 会话元信息 */}
            <div>
              <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase', marginBottom: 6 }}>
                会话元信息
              </Text>
              <Stack gap={4} style={{ fontSize: 12 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                  <span style={{ color: '#64748B' }}>智能体 (Agent):</span>
                  <span style={{ fontWeight: 500 }}>{selectedNode.agent?.name || '未知'}</span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                  <span style={{ color: '#64748B' }}>工作区:</span>
                  <span style={{ fontWeight: 500 }}>{selectedNode.session.workspace || '默认'}</span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                  <span style={{ color: '#64748B' }}>会话 ID:</span>
                  <span style={{ fontFamily: 'monospace', fontSize: 11 }}>{selectedNode.session.id.slice(0, 16)}...</span>
                </div>
              </Stack>
            </div>
          </div>

          {/* 检视舱底部操作区 */}
          <div
            style={{
              padding: '10px 14px',
              borderTop: '1px solid var(--astr-border, #F1F5F9)',
              background: 'var(--astr-surface-muted, #F8FAFC)',
              display: 'flex',
              flexDirection: 'column',
              gap: 8,
            }}
          >
            <Button
              color="indigo"
              fullWidth
              size="sm"
              leftSection={<IconTerminal2 size={16} />}
              onClick={() => onOpenSession(selectedNode.session)}
            >
              进入完整会话
            </Button>
            <Button
              variant="default"
              fullWidth
              size="sm"
              leftSection={<IconExternalLink size={16} />}
              onClick={() => setModalSession(selectedNode.session)}
            >
              弹窗查看现场
            </Button>
          </div>
        </aside>
      )}

      {/* 底部 HUD 战术图例栏 */}
      <div
        style={{
          position: 'absolute',
          bottom: 14,
          left: '50%',
          transform: 'translateX(-50%)',
          zIndex: 10,
          display: 'flex',
          alignItems: 'center',
          gap: 16,
          padding: '6px 16px',
          borderRadius: 99,
          background: 'var(--astr-surface, #ffffff)',
          border: '1px solid var(--astr-border, #e5e7eb)',
          boxShadow: '0 2px 8px rgba(0, 0, 0, 0.06)',
          fontSize: 12,
          color: 'var(--astr-text, #475569)',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onWheel={(e) => e.stopPropagation()}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#10B981' }} />
          运行中 ({runningCount})
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#F59E0B' }} />
          等待审批 ({waitingCount})
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#EF4444' }} />
          异常求援 ({sosCount})
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#94A3B8' }} />
          空闲就绪 ({idleCount})
        </span>
        <div style={{ width: 1, height: 14, background: 'var(--astr-border, #E5E7EB)' }} />
        <span style={{ fontSize: 11, color: '#94A3B8' }}>
          🖱️ 拖拽画布 · 滚轮缩放 · 点击卡片检视
        </span>
      </div>

      {/* 5. 全屏大板模态窗 (Blackboard Modal, 960px 宽度超大视口) */}
      <Modal
        opened={blackboardModalOpen}
        onClose={() => setBlackboardModalOpen(false)}
        size={980}
        radius="lg"
        centered
        title={
          <Group gap="xs">
            <IconActivity size={20} color="#5B5BD6" />
            <Text fw={700} size="md">
              星序 · 分布式作战黑板
            </Text>
            <SegmentedControl
              size="xs"
              value={blackboardScope}
              onChange={(val: string) => setBlackboardScope(val as 'swarm' | 'global')}
              data={[
                { label: `🪐 星系黑板 (${Object.keys(swarmBlackboard).length})`, value: 'swarm' },
                { label: `🌐 全局共享 (${Object.keys(globalBlackboard).length})`, value: 'global' },
              ]}
            />
          </Group>
        }
      >
        <div
          style={{
            maxHeight: 'calc(85vh - 120px)',
            overflowY: 'auto',
            paddingRight: 4,
          }}
          className="space-y-4"
        >
          {Object.entries(blackboardScope === 'swarm' ? swarmBlackboard : globalBlackboard).slice().reverse().map(([k, v]) => (
            <BlackboardItemRenderer
                      key={k}
                      itemKey={k}
                      itemValue={v}
                      namespace={blackboardScope === 'swarm' && constellationRootKey ? `swarm:${constellationRootKey}` : 'global'}
                      onUpdated={() => {
                        if (blackboardScope === 'swarm' && constellationRootKey) {
                          void api.getBlackboard(`swarm:${constellationRootKey}`).then((res) => setSwarmBlackboard(res.items || {}))
                        } else {
                          void api.getBlackboard('global').then((res) => setGlobalBlackboard(res.items || {}))
                        }
                      }}
                    />
          ))}
        </div>
      </Modal>

      {/* 删除星系链确认弹窗 */}
      <Modal
        opened={Boolean(deleteModalData)}
        onClose={() => !isDeleting && setDeleteModalData(null)}
        title={
          <Group gap="xs">
            <IconAlertTriangle size={20} color="#DC2626" />
            <Text fw={700} size="md" c="red">
              删除星系及全链条会话
            </Text>
          </Group>
        }
        radius="lg"
        centered
        size="md"
      >
        {deleteModalData && (
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              您正在请求删除由主星【<strong style={{ color: 'var(--astr-text, #1E293B)' }}>{deleteModalData.rootNode.session.title || '主星会话'}</strong>】发起的完整星系任务链。
            </Text>

            <div
              style={{
                padding: '10px 12px',
                borderRadius: 8,
                background: '#FEF2F2',
                border: '1px solid #FECACA',
                fontSize: 12,
                color: '#991B1B',
              }}
            >
              <div style={{ fontWeight: 700, marginBottom: 4 }}>
                将级联执行以下清理（不可恢复）：
              </div>
              <ul style={{ margin: 0, paddingLeft: 18, lineHeight: 1.6 }}>
                <li>删除主星会话与 <strong>{deleteModalData.sessions.length - 1} 个关联伴星会话</strong>（共 {deleteModalData.sessions.length} 个）；</li>
                <li>彻底清空专属作战黑板（空间：<code style={{ fontFamily: 'monospace' }}>swarm:{deleteModalData.rootNode.key}</code>）；</li>
                <li>自动从监控室视图及待办队列中移除。</li>
              </ul>
            </div>

            <div style={{ maxHeight: 180, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 4 }}>
              {deleteModalData.sessions.map((item) => (
                <div
                  key={item.key}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '6px 10px',
                    borderRadius: 6,
                    background: 'var(--astr-surface-muted, #F8FAFC)',
                    border: '1px solid var(--astr-border, #E5E7EB)',
                    fontSize: 11.5,
                  }}
                >
                  <Group gap={6} style={{ minWidth: 0 }}>
                    <AgentBrandIcon kind={item.agent?.kind} size={15} />
                    <span style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 240 }}>
                      {item.session.title || '会话'}
                    </span>
                  </Group>
                  <Badge size="xs" variant="light" color={item.level === 0 ? 'indigo' : 'gray'}>
                    {item.level === 0 ? '🌟 主星' : `🪐 伴星 L${item.level}`}
                  </Badge>
                </div>
              ))}
            </div>

            <Group justify="flex-end" gap="sm" mt="xs">
              <Button
                variant="default"
                size="sm"
                onClick={() => setDeleteModalData(null)}
                disabled={isDeleting}
              >
                取消
              </Button>
              <Button
                color="red"
                size="sm"
                loading={isDeleting}
                onClick={async () => {
                  setIsDeleting(true)
                  try {
                    const toDelete = deleteModalData.sessions.map((s) => ({
                      agent_id: s.session.agent_id,
                      id: s.session.id,
                    }))
                    await api.batchDeleteSessions(toDelete)
                    await api.deleteBlackboard('*', `swarm:${deleteModalData.rootNode.key}`)

                    if (onRemoveFromMonitor) {
                      deleteModalData.sessions.forEach((s) => onRemoveFromMonitor(s.key))
                    }
                    if (selectedKey && deleteModalData.sessions.some((s) => s.key === selectedKey)) {
                      setSelectedKey(null)
                    }
                    await queryClient.invalidateQueries({ queryKey: ['astrorder', 'sessions'] })
                    notifications.show({
                      color: 'teal',
                      message: `已彻底删除星系「${deleteModalData.rootNode.session.title || '主星'}」及下辖所有伴星与黑板`,
                    })
                    setDeleteModalData(null)
                  } catch (err: any) {
                    notifications.show({
                      color: 'red',
                      message: err.message || '删除失败，请重试',
                    })
                  } finally {
                    setIsDeleting(false)
                  }
                }}
              >
                确认删除整个星系
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>

      {/* 点击卡片弹出完整会话浮窗 (Modal) */}
      <Modal
        opened={Boolean(modalSession)}
        onClose={() => setModalSession(null)}
        size={880}
        radius="lg"
        centered
        title={
          modalSession ? (
            <Group gap="xs">
              <AgentBrandIcon kind={agents[modalSession.agent_id]?.kind} size={22} />
              <Text fw={700} size="md" lineClamp={1}>
                {modalSession.title || '会话现场'}
              </Text>
              <Badge
                size="sm"
                variant="light"
                color={modalSession.parent_session_id ? 'cyan' : 'indigo'}
              >
                {modalSession.parent_session_id ? '🪐 伴星会话' : '🌟 主星会话'}
              </Badge>
            </Group>
          ) : null
        }
      >
        {modalSession && (
          <div style={{ height: 640 }}>
            <MonitorCard
              session={modalSession}
              agent={agents[modalSession.agent_id]}
              slotIndex={0}
              slotSize={{ width: 840, height: 640 }}
              onOpen={() => {
                setModalSession(null)
                onOpenSession(modalSession)
              }}
            />
          </div>
        )}
      </Modal>

      <style>{`
        @keyframes conduitFlow {
          from { stroke-dashoffset: 24; }
          to { stroke-dashoffset: 0; }
        }
        .conduit-flow {
          stroke-dasharray: 6 6;
          animation: conduitFlow 1.6s linear infinite;
        }
        .dot-grid {
          background-size: 24px 24px;
          background-image: radial-gradient(circle, var(--astr-border, #E2E8F0) 1.2px, transparent 1.2px);
        }
        @keyframes emeraldPulse {
          0%, 100% { opacity: 1; transform: scale(1); }
          50% { opacity: 0.4; transform: scale(1.2); }
        }
        .pulsing-emerald-dot {
          animation: emeraldPulse 2s infinite ease-in-out;
        }
        .swarm-node-card:hover {
          transform: translateY(-2px);
          box-shadow: 0 6px 20px rgba(0, 0, 0, 0.08) !important;
        }
      `}</style>
    </div>
  )
}
