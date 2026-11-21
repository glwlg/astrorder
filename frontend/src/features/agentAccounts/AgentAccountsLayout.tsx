import { NavLink, Outlet } from 'react-router-dom'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { accountPlatforms } from './accountPlatforms'
import './AgyAccountsPage.css'

export function AgentAccountsLayout() {
  return <div className="route-page agent-accounts-layout">
    <h1>Agent 账号管理</h1>
    <div className="agent-accounts-columns">
      <nav className="account-platforms" aria-label="账号平台">
        <span className="account-platforms-label">平台</span>
        {accountPlatforms.map(platform => <NavLink end key={platform.kind} to={`/agent-accounts${platform.path && `/${platform.path}`}`}>
          <AgentBrandIcon kind={platform.kind} size={21} /><span>{agentKindLabel(platform.kind)}</span>
          {!platform.Page && <small>未接入</small>}
        </NavLink>)}
      </nav>
      <div className="account-platform-content"><Outlet /></div>
    </div>
  </div>
}
