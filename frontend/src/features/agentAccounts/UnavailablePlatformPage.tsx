import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import type { AgentKind } from '../../domain/types'

export function UnavailablePlatformPage({ kind }: { kind: AgentKind }) {
  return <section className="account-platform-unavailable">
    <AgentBrandIcon kind={kind} size={32} />
    <h2>{agentKindLabel(kind)}</h2><p>此平台尚未接入账号管理。</p>
  </section>
}
