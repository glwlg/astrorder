import { useState } from 'react'
import { notifications } from '@mantine/notifications'
import type { Approval, Session } from '../../domain/types'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'

export function MobileApprovals({ session, approvals }: { session: Session; approvals: Approval[] }) {
  const [busy, setBusy] = useState<string | null>(null)
  const respond = async (approval: Approval, action: 'approve' | 'cancel') => {
    if (!approval.target_id || busy) return
    setBusy(approval.id)
    try {
      const result = await api.createCommand({ id: crypto.randomUUID(), agent_id: session.agent_id, session_id: session.id, action, target_id: approval.target_id, text: '', attachment_ids: [] })
      useAstrorderStore.getState().mergeCommands([result])
      if (result.state === 'failed' || result.state === 'unknown') throw new Error(result.error || '原生授权尚未确认')
    } catch (error) { notifications.show({ message: error instanceof Error ? error.message : '授权请求失败', color: 'red' }) }
    finally { setBusy(null) }
  }
  return <div className="m-native-approvals">{approvals.map(approval => <section key={approval.id} aria-label="待处理审批">
    <h3>{approval.title}</h3><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxHeight: 160, overflow: 'auto' }}>{approval.detail}</pre>
    <button disabled={Boolean(busy)} onClick={() => void respond(approval, 'approve')}>允许本次</button>
    <button disabled={Boolean(busy)} onClick={() => void respond(approval, 'cancel')}>拒绝</button>
  </section>)}</div>
}
