import type { Session } from './types'
import { isEphemeralSession, scopeKey } from './semantics'

export interface SwarmConstellationNode {
  key: string
  session: Session
  level: number
  parentKey: string | null
  childrenKeys: string[]
}

export interface SwarmConstellationEdge {
  id: string
  fromKey: string
  toKey: string
}

export interface SwarmStarRoot {
  key: string
  session: Session
  workerCount: number
  maxLevel: number
  childrenKeys: string[]
  descendantKeys: string[]
}

export interface SwarmConstellation {
  rootKey: string | null
  rootSession: Session | null
  nodes: SwarmConstellationNode[]
  edges: SwarmConstellationEdge[]
  workerCount: number
  isIncompleteSubtree?: boolean
}

export interface SwarmTopology {
  // 合作主星列表：自身无委派 parent 且拥有显式后代
  roots: SwarmStarRoot[]
  totalRootCount: number
  totalWorkerCount: number
  sessionMap: Map<string, Session>
  parentMap: Map<string, string>
  childrenMap: Map<string, string[]>
  // 记录因父记录缺失而被合理隐藏的不完整子树节点（不猜测 agent，不挂载孤枝）
  orphanedSubtreeKeys: Set<string>
  getConstellation: (targetRootKey?: string | null) => SwarmConstellation
}

/**
 * 纯拓扑计算 Helper：
 * 1. 严格使用 delegation_parent_agent_id 与 delegation_parent_session_id 推断显式协作边；
 * 2. 绝不 fallback 到 parent_session_id 或 handoff_from；
 * 3. 过滤 ephemeral / [侧边聊天]；
 * 4. 防环保护 (DAG / Cycle Prevention)；
 * 5. 缺失父记录时不随意猜测 agent，合理隐藏不完整子树；
 * 6. 协作根必须有显式后代且自身无委派 parent；纯标题含主星/群星/作战指令的孤点不入星系主星列表；
 * 7. 显式 URL 传入的 activeRootKey 若为孤点，允许在画布展示单节点，但不进入左栏主星列表。
 */
export function buildSwarmTopology(
  sessions: Session[],
  _options?: { activeRootKey?: string | null }
): SwarmTopology {
  const sessionMap = new Map<string, Session>()
  const parentMap = new Map<string, string>()
  const childrenMap = new Map<string, string[]>()
  const hasDeclaredDelegationParent = new Set<string>()

  // 1. 过滤掉临时会话并构建会话映射
  for (const s of sessions) {
    if (isEphemeralSession(s)) continue
    const key = scopeKey(s.agent_id, s.id)
    sessionMap.set(key, s)
    childrenMap.set(key, [])
  }

  // 2. 识别显式委派边（仅限 delegation_parent_* 字段）
  for (const s of sessionMap.values()) {
    const childKey = scopeKey(s.agent_id, s.id)
    const parentAgentId = s.delegation_parent_agent_id
    const parentSessionId = s.delegation_parent_session_id

    // 只有在明确提供 delegation 字段时才视作显式协作边
    if (parentAgentId && parentSessionId) {
      hasDeclaredDelegationParent.add(childKey)
      const parentKey = scopeKey(parentAgentId, parentSessionId)

      // 自环跳过
      if (parentKey === childKey) continue

      // 缺失父记录时不猜测 agent，放弃连接
      if (!sessionMap.has(parentKey)) {
        continue
      }

      // 防环检测：向上回溯 parentMap，若回到 childKey 则放弃此边以防死循环
      let curr: string | undefined = parentKey
      let createsCycle = false
      const visitedAncestors = new Set<string>()
      while (curr) {
        if (curr === childKey) {
          createsCycle = true
          break
        }
        if (visitedAncestors.has(curr)) break
        visitedAncestors.add(curr)
        curr = parentMap.get(curr)
      }

      if (!createsCycle) {
        parentMap.set(childKey, parentKey)
        const list = childrenMap.get(parentKey) || []
        list.push(childKey)
        childrenMap.set(parentKey, list)
      }
    }
  }

  // 3. 统计不完整子树（声明了父记录但无法连上任何有效根节点）
  const orphanedSubtreeKeys = new Set<string>()
  for (const s of sessionMap.values()) {
    const key = scopeKey(s.agent_id, s.id)
    if (hasDeclaredDelegationParent.has(key)) {
      // 检查回溯是否能到达一个没有委派 parent 的根节点
      let curr: string | undefined = key
      let hasValidRoot = false
      const traceVisited = new Set<string>()
      while (curr) {
        if (traceVisited.has(curr)) break
        traceVisited.add(curr)
        const pKey = parentMap.get(curr)
        if (!pKey) {
          // 如果 curr 自己没有 parent，但它是声明了 delegation_parent 的起始节点，说明断裂了
          if (curr === key && !parentMap.has(key)) {
            hasValidRoot = false
          } else if (!hasDeclaredDelegationParent.has(curr)) {
            hasValidRoot = true
          }
          break
        }
        curr = pKey
      }
      if (!hasValidRoot) {
        orphanedSubtreeKeys.add(key)
      }
    }
  }

  // 4. 识别合法协作根：
  // 条件：自身未声明 delegation_parent，且在 DAG 中拥有至少 1 个显式后代
  const roots: SwarmStarRoot[] = []
  let totalWorkerCount = 0

  for (const s of sessionMap.values()) {
    const key = scopeKey(s.agent_id, s.id)
    // 自身被委派者不可为主星
    if (hasDeclaredDelegationParent.has(key) || parentMap.has(key)) continue

    const directChildren = childrenMap.get(key) || []
    if (directChildren.length === 0) {
      // 无后代的孤立节点：标题哪怕含有“主星/群星/作战指令”也不作为协作根入图/入侧栏
      continue
    }

    // BFS 遍历子树，统计深度和伴星总数
    const descendantKeys: string[] = []
    const visited = new Set<string>([key])
    const queue: Array<{ key: string; level: number }> = [{ key, level: 0 }]
    let maxLevel = 0

    while (queue.length > 0) {
      const item = queue.shift()!
      if (item.level > maxLevel) maxLevel = item.level
      const children = childrenMap.get(item.key) || []
      for (const cKey of children) {
        if (!visited.has(cKey)) {
          visited.add(cKey)
          descendantKeys.push(cKey)
          queue.push({ key: cKey, level: item.level + 1 })
        }
      }
    }

    const workerCount = descendantKeys.length
    if (workerCount > 0) {
      totalWorkerCount += workerCount
      roots.push({
        key,
        session: s,
        workerCount,
        maxLevel,
        childrenKeys: directChildren,
        descendantKeys,
      })
    }
  }

  // 根节点排序：活跃在前，更新时间倒序
  roots.sort((a, b) => {
    const aActive = a.session.status === 'running' || a.session.status === 'waiting_approval' ? 1 : 0
    const bActive = b.session.status === 'running' || b.session.status === 'waiting_approval' ? 1 : 0
    if (aActive !== bActive) return bActive - aActive
    return (b.session.updated_at || '').localeCompare(a.session.updated_at || '')
  })

  // 5. 提取单星系拓扑（用于画布渲染）
  const getConstellation = (targetRootKey?: string | null): SwarmConstellation => {
    let activeKey: string | null = null

    if (targetRootKey && sessionMap.has(targetRootKey)) {
      activeKey = targetRootKey
      // 子会话入口只改变查看目标；画布始终保留其所属星系的完整层级。
      const ancestors = new Set<string>()
      while (parentMap.has(activeKey) && !ancestors.has(activeKey)) {
        ancestors.add(activeKey)
        activeKey = parentMap.get(activeKey)!
      }
    } else if (roots.length > 0) {
      activeKey = roots[0].key
    }

    if (!activeKey) {
      return {
        rootKey: null,
        rootSession: null,
        nodes: [],
        edges: [],
        workerCount: 0,
      }
    }

    const rootSession = sessionMap.get(activeKey)!
    const nodes: SwarmConstellationNode[] = []
    const edges: SwarmConstellationEdge[] = []

    // 层次遍历构建节点与边
    const visited = new Set<string>([activeKey])
    const queue: Array<{ key: string; level: number; parentKey: string | null }> = [
      { key: activeKey, level: 0, parentKey: null },
    ]

    while (queue.length > 0) {
      const curr = queue.shift()!
      const currSession = sessionMap.get(curr.key)
      if (!currSession) continue

      const rawChildren = childrenMap.get(curr.key) || []
      const validChildren: string[] = []

      for (const childKey of rawChildren) {
        if (!visited.has(childKey) && sessionMap.has(childKey)) {
          visited.add(childKey)
          validChildren.push(childKey)
          queue.push({ key: childKey, level: curr.level + 1, parentKey: curr.key })
          edges.push({
            id: `${curr.key}->${childKey}`,
            fromKey: curr.key,
            toKey: childKey,
          })
        }
      }

      nodes.push({
        key: curr.key,
        session: currSession,
        level: curr.level,
        parentKey: curr.parentKey,
        childrenKeys: validChildren,
      })
    }

    const workerCount = Math.max(0, nodes.length - 1)
    return {
      rootKey: activeKey,
      rootSession,
      nodes,
      edges,
      workerCount,
      isIncompleteSubtree: orphanedSubtreeKeys.has(activeKey),
    }
  }

  return {
    roots,
    totalRootCount: roots.length,
    totalWorkerCount,
    sessionMap,
    parentMap,
    childrenMap,
    orphanedSubtreeKeys,
    getConstellation,
  }
}
