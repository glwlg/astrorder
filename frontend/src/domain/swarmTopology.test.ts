import { describe, expect, it } from 'vitest'
import type { Session } from './types'
import { buildSwarmTopology } from './swarmTopology'

function makeSession(partial: Partial<Session> & { id: string; agent_id: string }): Session {
  return {
    id: partial.id,
    agent_id: partial.agent_id,
    title: partial.title ?? `Session ${partial.id}`,
    workspace: '/repo',
    status: partial.status ?? 'idle',
    updated_at: partial.updated_at ?? '2026-10-19T10:00:00Z',
    ephemeral: partial.ephemeral,
    delegation_parent_agent_id: partial.delegation_parent_agent_id,
    delegation_parent_session_id: partial.delegation_parent_session_id,
    parent_session_id: partial.parent_session_id,
    parent_agent_id: partial.parent_agent_id,
    handoff_from_agent_id: partial.handoff_from_agent_id,
    handoff_from_session_id: partial.handoff_from_session_id,
  }
}

describe('swarmTopology', () => {
  it('identifies 3-level hierarchy A -> B -> C with ordinary titles', () => {
    const sessionA = makeSession({
      id: 'session-a',
      agent_id: 'agent-1',
      title: '常规业务处理会话',
    })
    const sessionB = makeSession({
      id: 'session-b',
      agent_id: 'agent-2',
      title: '委派子步骤分析',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'session-a',
    })
    const sessionC = makeSession({
      id: 'session-c',
      agent_id: 'agent-3',
      title: '孙会话执行深层编译',
      delegation_parent_agent_id: 'agent-2',
      delegation_parent_session_id: 'session-b',
    })

    const topology = buildSwarmTopology([sessionA, sessionB, sessionC])

    expect(topology.totalRootCount).toBe(1)
    expect(topology.totalWorkerCount).toBe(2)

    const root = topology.roots[0]
    expect(root.key).toBe('agent-1::session-a')
    expect(root.workerCount).toBe(2)
    expect(root.maxLevel).toBe(2)
    expect(root.childrenKeys).toEqual(['agent-2::session-b'])

    // 画布获取星系拓扑
    const constellation = topology.getConstellation('agent-1::session-a')
    expect(constellation.rootKey).toBe('agent-1::session-a')
    expect(constellation.nodes).toHaveLength(3)

    const nodeA = constellation.nodes.find((n) => n.key === 'agent-1::session-a')!
    const nodeB = constellation.nodes.find((n) => n.key === 'agent-2::session-b')!
    const nodeC = constellation.nodes.find((n) => n.key === 'agent-3::session-c')!

    expect(nodeA.level).toBe(0)
    expect(nodeB.level).toBe(1)
    expect(nodeC.level).toBe(2)
    expect(nodeA.parentKey).toBeNull()
    expect(nodeB.parentKey).toBe('agent-1::session-a')
    expect(nodeC.parentKey).toBe('agent-2::session-b')

    expect(constellation.edges).toEqual([
      { id: 'agent-1::session-a->agent-2::session-b', fromKey: 'agent-1::session-a', toKey: 'agent-2::session-b' },
      { id: 'agent-2::session-b->agent-3::session-c', fromKey: 'agent-2::session-b', toKey: 'agent-3::session-c' },
    ])
  })

  it.each(['b', 'c'])('resolves a child entry %s to the complete constellation', target => {
    const root = makeSession({ id: 'a', agent_id: 'root-agent' })
    const child = makeSession({ id: 'b', agent_id: 'worker-agent', delegation_parent_agent_id: root.agent_id, delegation_parent_session_id: root.id })
    const grandchild = makeSession({ id: 'c', agent_id: 'worker-agent', delegation_parent_agent_id: child.agent_id, delegation_parent_session_id: child.id })
    const topology = buildSwarmTopology([grandchild, child, root])
    const constellation = topology.getConstellation(`worker-agent::${target}`)
    expect(constellation.rootKey).toBe('root-agent::a')
    expect(constellation.nodes.map(node => [node.key, node.level])).toEqual([
      ['root-agent::a', 0], ['worker-agent::b', 1], ['worker-agent::c', 2],
    ])
    expect(constellation.edges).toHaveLength(2)
    expect(constellation.workerCount).toBe(2)
  })

  it('excludes fork and handoff sessions from swarm topology without explicit delegation', () => {
    const parentSession = makeSession({
      id: 'parent-1',
      agent_id: 'agent-1',
      title: '主工作区会话',
    })
    const forkedSession = makeSession({
      id: 'forked-1',
      agent_id: 'agent-1',
      title: 'Fork 分支会话',
      parent_session_id: 'parent-1',
      parent_agent_id: 'agent-1',
    })
    const handoffSession = makeSession({
      id: 'handoff-1',
      agent_id: 'agent-2',
      title: '工作转交会话',
      handoff_from_agent_id: 'agent-1',
      handoff_from_session_id: 'parent-1',
    })

    const topology = buildSwarmTopology([parentSession, forkedSession, handoffSession])

    // 无任何显式 delegation 边，故没有任何主星或伴星
    expect(topology.totalRootCount).toBe(0)
    expect(topology.totalWorkerCount).toBe(0)
    expect(topology.roots).toHaveLength(0)
    expect(topology.parentMap.size).toBe(0)
  })

  it('excludes isolated sessions with swarm/star titles if they have no explicit descendants', () => {
    const standalone1 = makeSession({
      id: 's-1',
      agent_id: 'agent-1',
      title: '主星协同任务中心',
    })
    const standalone2 = makeSession({
      id: 's-2',
      agent_id: 'agent-1',
      title: '群星作战指令调度器',
    })
    const standalone3 = makeSession({
      id: 's-3',
      agent_id: 'agent-2',
      title: '星系中枢',
    })

    const topology = buildSwarmTopology([standalone1, standalone2, standalone3])

    // 虽含有主星/群星等标题关键词，但无显式委派后代，绝不入选左侧主星栏
    expect(topology.roots).toHaveLength(0)
    expect(topology.totalRootCount).toBe(0)
  })

  it('does not repair whitespace in explicit parent identifiers', () => {
    const root = makeSession({ id: 'root', agent_id: 'agent-1' })
    const child = makeSession({
      id: 'child',
      agent_id: 'agent-1',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: ' root ',
    })
    const topology = buildSwarmTopology([root, child])
    expect(topology.totalRootCount).toBe(0)
    expect(topology.parentMap.get('agent-1::child')).toBeUndefined()
  })

  it('keeps sessions with same native ID isolated under different agents', () => {
    // 两个不同 agent 拥有相同本地 id 的会话
    const a1Session = makeSession({
      id: 'uuid-100',
      agent_id: 'agent-codex',
      title: 'Codex 会话',
    })
    const a2Session = makeSession({
      id: 'uuid-100',
      agent_id: 'agent-hermes',
      title: 'Hermes 会话',
    })
    const childOfCodex = makeSession({
      id: 'child-1',
      agent_id: 'agent-worker',
      title: 'Codex 子会话',
      delegation_parent_agent_id: 'agent-codex',
      delegation_parent_session_id: 'uuid-100',
    })

    const topology = buildSwarmTopology([a1Session, a2Session, childOfCodex])

    expect(topology.totalRootCount).toBe(1)
    expect(topology.roots[0].key).toBe('agent-codex::uuid-100')
    expect(topology.roots[0].workerCount).toBe(1)
    expect(topology.parentMap.get('agent-worker::child-1')).toBe('agent-codex::uuid-100')
    expect(topology.parentMap.get('agent-worker::child-1')).not.toBe('agent-hermes::uuid-100')
  })

  it('filters out ephemeral and side-chat sessions completely', () => {
    const root = makeSession({
      id: 'root-1',
      agent_id: 'agent-1',
      title: '主会话',
    })
    const ephemeralChild = makeSession({
      id: 'e-1',
      agent_id: 'agent-1',
      title: '临时测试',
      ephemeral: true,
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'root-1',
    })
    const sideChatChild = makeSession({
      id: 's-1',
      agent_id: 'agent-1',
      title: '[侧边聊天]',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'root-1',
    })

    const topology = buildSwarmTopology([root, ephemeralChild, sideChatChild])

    expect(topology.totalRootCount).toBe(0)
    expect(topology.roots).toHaveLength(0)
    expect(topology.sessionMap.has('agent-1::e-1')).toBe(false)
    expect(topology.sessionMap.has('agent-1::s-1')).toBe(false)
  })

  it('prevents cycles and does not enter infinite recursion', () => {
    const sessionA = makeSession({
      id: 'a',
      agent_id: 'agent-1',
      title: '会话A',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'c', // C -> A 产生环
    })
    const sessionB = makeSession({
      id: 'b',
      agent_id: 'agent-1',
      title: '会话B',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'a',
    })
    const sessionC = makeSession({
      id: 'c',
      agent_id: 'agent-1',
      title: '会话C',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'b',
    })

    // 不应陷入死循环
    const topology = buildSwarmTopology([sessionA, sessionB, sessionC])
    expect(topology).toBeDefined()
    // 环被安全打破，保证 DAG
  })

  it('hides incomplete subtrees with missing parent records without guessing agent', () => {
    // 存在一个 session-missing，但是在 agent-other 下，并不是声明的 agent-parent
    const otherSession = makeSession({
      id: 'session-missing',
      agent_id: 'agent-other',
      title: '巧合同名会话',
    })
    const danglingChild = makeSession({
      id: 'child-orphan',
      agent_id: 'agent-worker',
      title: '孤立子会话',
      delegation_parent_agent_id: 'agent-parent', // 缺失的 agent
      delegation_parent_session_id: 'session-missing',
    })
    const grandchild = makeSession({
      id: 'child-grand',
      agent_id: 'agent-worker',
      title: '孙会话',
      delegation_parent_agent_id: 'agent-worker',
      delegation_parent_session_id: 'child-orphan',
    })

    const topology = buildSwarmTopology([otherSession, danglingChild, grandchild])

    // 不随意挂载到 agent-other 上
    expect(topology.parentMap.get('agent-worker::child-orphan')).toBeUndefined()
    // 缺失父记录时不作为主星，整个不完整子树合理隐藏
    expect(topology.totalRootCount).toBe(0)
    expect(topology.roots).toHaveLength(0)
    expect(topology.orphanedSubtreeKeys.has('agent-worker::child-orphan')).toBe(true)
  })

  it('allows explicit URL root isolated node to be displayed on canvas without entering left rail', () => {
    const standalone = makeSession({
      id: 'solo-1',
      agent_id: 'agent-1',
      title: '独立会话',
    })

    const topology = buildSwarmTopology([standalone])

    // 左栏 roots 列表为空（因为伴星数为 0 且无后代）
    expect(topology.roots).toHaveLength(0)

    // 但是如果 URL 显式指定 ?root=agent-1::solo-1，画布可以展示该单节点
    const canvasConstellation = topology.getConstellation('agent-1::solo-1')
    expect(canvasConstellation.rootKey).toBe('agent-1::solo-1')
    expect(canvasConstellation.nodes).toHaveLength(1)
    expect(canvasConstellation.nodes[0].key).toBe('agent-1::solo-1')
    expect(canvasConstellation.workerCount).toBe(0)
  })
})
