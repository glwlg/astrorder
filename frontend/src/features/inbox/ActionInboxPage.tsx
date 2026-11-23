import { useNavigate } from 'react-router-dom'
import { ActionInboxView } from './ActionInboxView'

export function ActionInboxPage() {
  const navigate = useNavigate()
  return (
    <div className="action-inbox-page">
      <ActionInboxView
        onOpenSession={(agentId, sessionId) => {
          navigate(`/chat/${encodeURIComponent(sessionId)}?agent_id=${encodeURIComponent(agentId)}`)
        }}
      />
    </div>
  )
}
