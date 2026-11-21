import { Button, HoverCard } from '@mantine/core'
import { useState, type ReactElement, type ReactNode } from 'react'
import type { Agent, Session } from '../../domain/types'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { connectionName } from '../../components/AgentSessionFilter'
import { AgyAccountDialog } from './AgyAccountDialog'
import { AgyQuotaSummary } from './AgyQuotaSummary'
import type { AgyAccount } from './agyAccountsApi'
import { useRefreshSettings, useScheduledRefresh } from './accountRefresh'
import { useAgyAccountMutation } from './useAgyAccountMutation'
import { useAgyAccountOverview } from './useAgyAccountOverview'
import './AgyAccountsPage.css'
import './sessionAccountsHoverCard.css'

function AccountCard({ link, title, children }: { link: ReactElement; title: string; children: ReactNode }) {
  return <HoverCard interactive position="right-start" width="min(448px, calc(100vw - 24px))" withArrow
    openDelay={150} closeDelay={180} transitionProps={{ duration: 0 }}>
    <HoverCard.Target>{link}</HoverCard.Target>
    <HoverCard.Dropdown aria-label={title} className="session-account-card">{children}</HoverCard.Dropdown>
  </HoverCard>
}

export function SessionAccountsHoverCard({ link, session, agent }: { link: ReactElement; session?: Session; agent?: Agent }) {
  const kind = agent?.kind.replace(/-ssh$/, '')
  const environment = session?.connection_id || agent?.connection_id || (agent?.kind.endsWith('-ssh') ? '' : 'local')
  const mismatch = session?.connection_id && agent?.connection_id && session.connection_id !== agent.connection_id
  if (session && agent && kind === 'antigravity' && environment && !mismatch) {
    const machine = environment === 'local' ? '本机' : agent.connection_id ? connectionName(agent) : environment
    return <AgySessionCard key={JSON.stringify([session.agent_id, session.id, environment])} link={link}
      environment={environment} machine={machine} active={session.status === 'running' || session.status === 'waiting_approval'} />
  }
  const message = !session ? '选择会话后可查看对应平台的账号额度。' : !agent || !kind ? '当前会话的 Agent 信息不可用。' :
    kind === 'antigravity' ? '无法确定当前会话的目标机器。' : `${agentKindLabel(kind)} 尚未接入账号管理。`
  return <AccountCard link={link} title="会话账号额度"><span className="agy-account-muted">{message}</span></AccountCard>
}

type ConfirmSwitch = { account: AgyAccount; unsaved: boolean }

function AgySessionCard({ link, environment, machine, active }: { link: ReactElement; environment: string; machine: string; active: boolean }) {
  const mutation = useAgyAccountMutation(environment)
  const [confirmation, setConfirmation] = useState<ConfirmSwitch | null>(null)
  return <>
    <AccountCard link={link} title={`Antigravity · ${machine} 账号额度`}>
      <AgyPreview environment={environment} machine={machine} busy={mutation.isPending} error={mutation.error?.message}
        onSwitch={(account, unsaved) => {
          if (unsaved || active) setConfirmation({ account, unsaved })
          else mutation.mutate({ kind: 'switch', id: account.id, force: false })
        }} />
    </AccountCard>
    {confirmation && <AgyAccountDialog prompt={{ kind: 'switch', account: confirmation.account }} targetName={`Antigravity · ${machine}`}
      unsavedCurrent={confirmation.unsaved} onClose={() => setConfirmation(null)}
      onSwitch={(id, force) => mutation.mutate({ kind: 'switch', id, force })} />}
  </>
}

function AgyPreview({ environment, machine, busy, error, onSwitch }: {
  environment: string; machine: string; busy: boolean; error?: string; onSwitch: (account: AgyAccount, unsaved: boolean) => void
}) {
  const { accounts, state, rows, quotas, refresh, refreshing } = useAgyAccountOverview(environment, true)
  const [settings] = useRefreshSettings('antigravity')
  useScheduledRefresh(environment, settings, mode => { if (!refreshing && !busy) void refresh(mode) })
  return <>
    <header className="session-account-heading">
      <AgentBrandIcon kind="antigravity" size={22} />
      <strong>Antigravity 额度</strong><small title={machine}>{machine}</small>
      <Button variant="subtle" size="compact-sm" loading={refreshing} disabled={busy} onClick={() => void refresh('all')}>刷新额度</Button>
    </header>
    <div className="session-account-scroll">
      {accounts.isPending && <p role="status">正在读取账号…</p>}
      {accounts.isError && <p role="alert">{accounts.error.message}</p>}
      {state.isError && <p role="alert">{state.error.message}</p>}
      {error && <p role="alert">{error}</p>}
      {busy && <p role="status">正在更新目标 Agent 的账号…</p>}
      {accounts.isSuccess && !state.isPending && !rows.length && <p>暂无账号，可在账号管理中导入。</p>}
      <ul className="session-account-list" aria-label="当前平台的账号">{rows.map((account, i) => {
        const quota = quotas[i]
        const plan = quota.data?.plan || quota.data?.tier_id
        return <li key={account.id} data-current={account.current || undefined}>
          <div className="session-account-row">
            <strong title={account.name}>{account.name}</strong>
            {plan && <span className="session-account-tier" title={plan}>{plan.replace(/^Google AI /i, '')}</span>}
            {account.current ? <span className="agy-current-badge">当前使用</span> :
              <Button size="compact-sm" variant="light" aria-label={`切换到 ${account.name}`} disabled={!state.isSuccess || busy || refreshing}
                onClick={() => onSwitch(account, state.data?.current_account_id === 'current')}>切换</Button>}
          </div>
          {quota.data ? <AgyQuotaSummary quota={quota.data} /> :
            <small>{state.data?.quota_supported === false ? '此系统暂不支持额度查询' : quota.isFetching ? '正在读取额度…' : '尚未刷新'}</small>}
          {quota.isError && <small role="status">{quota.error.message}</small>}
        </li>
      })}</ul>
    </div>
  </>
}
