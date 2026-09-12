import { useEffect, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { api } from '../../../../api/client'
import type { Session } from '../../../../domain/types'
import { scopeKey } from '../../../../domain/semantics'
import { selectApprovals, selectMessages, selectOutbox, useAstrorderStore } from '../../../../state/store'
import { ChatComposer } from '../../../chat/ChatComposer'
import { Transcript } from '../../../chat/Transcript'
import type { ViewerContext } from '../../types'

/**
 * Codex 风格临时侧边会话：
 * - 打开时只显示空的 fork transcript，不把主会话历史渲染进来
 * - Composer 直接复用主会话 ChatComposer，保持附件、模型、审批、语音和发送行为一致
 * - 后端从主会话 fork 出 ephemeral thread，继承上下文但不进入主会话显示
 */
export function SideChatViewer({ artifact }: ViewerContext) {
  const parentSession = useAstrorderStore((state) => state.sessions[scopeKey(artifact.agentId, artifact.sessionId)])
  const agent = useAstrorderStore((state) => state.agents[artifact.agentId])
  const [sideSession, setSideSession] = useState<Session | null>(null)
  const [forkError, setForkError] = useState<string | null>(null)
  const [composerHeight, setComposerHeight] = useState(124)
  const forkRequest = useRef<Promise<Session> | null>(null)

  useEffect(() => {
    if (!parentSession || sideSession) return
    let cancelled = false
    setForkError(null)
    // StrictMode and live parent metadata updates must share one native fork.
    forkRequest.current ??= api.createSession({
      agent_id: parentSession.agent_id,
      workspace: parentSession.workspace,
      title: '[侧边聊天]',
      project_id: parentSession.project_id,
      project_name: parentSession.project_name,
      parent_session_id: parentSession.id,
      ephemeral: true,
    })
    void forkRequest.current.then((created) => {
      if (!cancelled) setSideSession(created)
    }).catch((error: unknown) => {
      if (!cancelled) setForkError(error instanceof Error ? error.message : '创建侧边会话失败。')
    })
    return () => { cancelled = true }
  }, [parentSession, sideSession])

  const sideAgentId = sideSession?.agent_id || artifact.agentId
  const sideSessionId = sideSession?.id || ''
  const messages = useAstrorderStore(useShallow((state) => selectMessages(state, sideAgentId, sideSessionId)))
  const outbox = useAstrorderStore(useShallow((state) => selectOutbox(state, sideAgentId, sideSessionId)))
  const approvals = useAstrorderStore(useShallow((state) => selectApprovals(state, sideAgentId, sideSessionId)))

  if (!sideSession) {
    return (
      <div className="sidechat-viewer sidechat-viewer-loading">
        {forkError || '正在准备侧边聊天…'}
      </div>
    )
  }

  return (
    <div className="chat-column sidechat-viewer">
      <Transcript
        session={sideSession}
        messages={messages}
        outbox={outbox}
        approvals={approvals}
        canApprove={false}
        composerHeight={composerHeight}
        loading={false}
      />
      <ChatComposer
        key={`side-composer:${sideSession.agent_id}:${sideSession.id}`}
        session={sideSession}
        agent={agent}
        onHeightChange={setComposerHeight}
      />
    </div>
  )
}
