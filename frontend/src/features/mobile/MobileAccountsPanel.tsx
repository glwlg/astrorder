import { useEffect, useState } from 'react'
import { Alert, Button, Loader } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery } from '@tanstack/react-query'
import { IconDeviceFloppy, IconPlus, IconRefresh, IconSettings, IconSwitchHorizontal } from '@tabler/icons-react'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { agyAccountsApi } from '../agentAccounts/agyAccountsApi'
import { AgyAccountDialog, type AccountPrompt } from '../agentAccounts/AgyAccountDialog'
import { AgyAccountImportDialog } from '../agentAccounts/AgyAccountImportDialog'
import { AgyQuotaTiles } from '../agentAccounts/AgyQuotaTiles'
import { agyStamp } from '../agentAccounts/agyQuotaFormat'
import { AccountRefreshSettings } from '../agentAccounts/AccountRefreshSettings'
import { useRefreshSettings } from '../agentAccounts/accountRefresh'
import { useAccountPageRefreshTarget } from '../agentAccounts/AgyQuotaRefreshProvider'
import { metadataPolicy, useAgyAccountOverview } from '../agentAccounts/useAgyAccountOverview'
import { useAgyAccountMutation } from '../agentAccounts/useAgyAccountMutation'
import '../agentAccounts/AgyAccountsPage.css'

export function MobileAccountsPanel({ preferredEnvironment = '' }: { preferredEnvironment?: string }) {
  const [manualEnvironment, setManualEnvironment] = useState<string | null>(null)
  const [dialog, setDialog] = useState<{ prompt: AccountPrompt; environment: string } | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useRefreshSettings('antigravity')

  const targets = useQuery({ ...metadataPolicy, queryKey: ['agy-account-targets'], queryFn: agyAccountsApi.targets })
  const targetList = targets.data?.items ?? []
  const target = (manualEnvironment ? targetList.find(item => item.id === manualEnvironment) : undefined)
    ?? (preferredEnvironment ? targetList.find(item => item.id === preferredEnvironment) : undefined)
    ?? targetList[0]
  const environment = target?.id ?? ''

  const { accounts, state: targetState, currentID, rows, quotas, refreshing, refresh, refreshLibrary } = useAgyAccountOverview(environment)
  const quotaSupported = targetState.data?.quota_supported
  const prompt = dialog?.environment === environment ? dialog.prompt : null

  useEffect(() => {
    if (environment && targetState.fetchStatus === 'idle' && !targetState.data && !targetState.isError) {
      void targetState.refetch()
    }
  }, [environment])

  const mutation = useAgyAccountMutation(environment)
  useAccountPageRefreshTarget(environment, Boolean(dialog || importOpen || settingsOpen))

  async function openAction(account?: typeof rows[number]) {
    const latest = await targetState.refetch({ cancelRefetch: false })
    if (latest.isError || !latest.data) return
    const active = latest.data.current_account_id
    if (account) {
      setDialog({ environment, prompt: { kind: 'switch', account: { ...account, current: active === account.id } } })
    } else if (active) {
      setDialog({ environment, prompt: { kind: 'save', name: rows.find(row => row.id === active && active !== 'current')?.name ?? '' } })
    } else {
      notifications.show({ message: '目标 Agent 尚未登录 AGY' })
    }
  }

  return (
    <div className="m-accounts-panel">
      <div className="m-accounts-toolbar">
        <div className="m-accounts-target-row">
          <label className="m-accounts-select">
            <span>
              <AgentBrandIcon kind="antigravity" size={14} /> Antigravity 目标机器
              {accounts.data ? ` · ${accounts.data.items.length} 个账号` : ''}
            </span>
            <select
              aria-label="目标 Agent"
              value={environment}
              disabled={mutation.isPending || !targetList.length}
              onChange={event => {
                mutation.reset()
                setManualEnvironment(event.currentTarget.value)
              }}
            >
              {!targetList.length && <option value="">暂无可用 AGY 目标</option>}
              {targetList.map(item => (
                <option key={item.id} value={item.id}>{item.name}</option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className="m-accounts-refresh-btn"
            aria-label="刷新全部"
            disabled={mutation.isPending || accounts.isPending || refreshing}
            onClick={() => {
              void targets.refetch()
              if (target) void refresh('all')
              else void accounts.refetch()
            }}
          >
            <IconRefresh size={18} className={refreshing ? 'm-spin' : undefined} />
          </button>
        </div>

        <div className="m-accounts-tool-chips">
          <button
            type="button"
            className="m-accounts-chip"
            disabled={!target || refreshing || mutation.isPending}
            onClick={() => void openAction()}
          >
            <IconDeviceFloppy size={14} />
            <span>保存当前账号</span>
          </button>
          <button
            type="button"
            className="m-accounts-chip"
            disabled={mutation.isPending}
            onClick={() => setImportOpen(true)}
          >
            <IconPlus size={14} />
            <span>导入账号</span>
          </button>
          <button
            type="button"
            className="m-accounts-chip"
            onClick={() => setSettingsOpen(true)}
          >
            <IconSettings size={14} />
            <span>刷新设置</span>
          </button>
        </div>
      </div>

      <div className="m-accounts-list">
        {targets.isError && (
          <Alert color="red" title="无法读取 Agent">
            {targets.error.message}{' '}
            <Button variant="subtle" size="compact-xs" onClick={() => void targets.refetch()}>重试</Button>
          </Alert>
        )}
        {accounts.isError && (
          <Alert color="red" title="无法读取账号">
            {accounts.error.message}{' '}
            <Button variant="subtle" size="compact-xs" onClick={() => void accounts.refetch()}>重试</Button>
          </Alert>
        )}
        {targetState.isError && (
          <Alert color="red" title="无法读取目标 Agent 的账号状态">
            {targetState.error.message}{' '}
            <Button variant="subtle" size="compact-xs" onClick={() => void targetState.refetch()}>重试</Button>
          </Alert>
        )}
        {mutation.isError && (
          <Alert color="red" title="账号操作未完成">{mutation.error.message}</Alert>
        )}
        {mutation.isPending && (
          <Alert role="status">
            {mutation.variables?.kind === 'save' ? '正在保存当前账号…' : '正在切换账号并重新接入 AGY…'}
          </Alert>
        )}
        {accounts.isPending && (
          <div className="m-accounts-empty" role="status">
            <Loader size="xs" color="violet" /> 正在读取账号…
          </div>
        )}
        {targets.isSuccess && !target && (
          <Alert>
            {targetList.length ? '所选目标不可用，请重新选择目标 Agent。' : '接入 AGY Agent 后即可切换账号和查询额度。'}
          </Alert>
        )}
        {accounts.isSuccess && !rows.length && (
          <div className="m-accounts-empty">
            暂无已保存账号，可点击上方「导入账号」或「保存当前账号」。
          </div>
        )}

        {accounts.isSuccess && rows.length > 0 && (
          <div className="m-accounts-cards" role="list" aria-label="AGY 账号列表">
            {rows.map((account, index) => {
              const quota = quotas[index]
              const plan = quota.data?.plan || quota.data?.tier_id
              return (
                <article
                  key={account.id}
                  role="listitem"
                  className="m-account-card"
                  data-current={account.current || undefined}
                >
                  <header className="m-account-card-head">
                    <div className="m-account-card-title">
                      <strong>{account.name}</strong>
                      <div className="m-account-card-badges">
                        {account.current && <span className="agy-current-badge">当前使用</span>}
                        {plan && (
                          <span className="m-account-tier" title={plan}>
                            {plan.replace(/^Google AI /i, '')}
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="m-account-card-time">
                      {quota.data ? (
                        <time dateTime={quota.data.as_of}>{agyStamp(quota.data.as_of)}</time>
                      ) : (
                        <span>—</span>
                      )}
                    </div>
                  </header>

                  <div className="m-account-card-body" aria-busy={quota.isFetching}>
                    {quota.error && (
                      <div className="agy-account-error" role="status">
                        <strong>额度查询失败</strong>
                        <span>{quota.error.message}</span>
                      </div>
                    )}
                    {quota.data ? (
                      <AgyQuotaTiles quota={quota.data} />
                    ) : !quota.error && (
                      <span className="m-account-muted">
                        {!target
                          ? '请选择目标 Agent'
                          : quotaSupported === false
                            ? '此系统暂不支持隔离查询'
                            : quota.isFetching
                              ? '正在读取额度…'
                              : '尚未刷新，点击下方刷新获取最新额度'}
                      </span>
                    )}
                  </div>

                  <footer className="m-account-card-actions">
                    {target && quotaSupported !== false && (
                      <Button
                        size="compact-sm"
                        variant="default"
                        leftSection={<IconRefresh size={14} />}
                        aria-label={`刷新 ${account.name} 的额度`}
                        loading={quota.isFetching}
                        disabled={mutation.isPending || refreshing}
                        onClick={() => void refresh({ id: account.id })}
                      >
                        刷新额度
                      </Button>
                    )}
                    {account.id !== 'current' && target && (
                      <Button
                        size="compact-sm"
                        variant={account.current ? 'light' : 'filled'}
                        color="violet"
                        leftSection={<IconSwitchHorizontal size={14} />}
                        aria-label={account.current ? `重新应用 ${account.name}` : `切换到 ${account.name}`}
                        disabled={mutation.isPending || refreshing}
                        onClick={() => void openAction(account)}
                      >
                        {account.current ? '重新应用' : '切换账号'}
                      </Button>
                    )}
                  </footer>
                </article>
              )
            })}
          </div>
        )}
      </div>

      {prompt && target && (
        <AgyAccountDialog
          prompt={prompt}
          targetName={target.name}
          unsavedCurrent={currentID === 'current'}
          onClose={() => setDialog(null)}
          onSave={name => mutation.mutate({ kind: 'save', name })}
          onSwitch={(id, force) => mutation.mutate({ kind: 'switch', id, force })}
        />
      )}
      {importOpen && (
        <AgyAccountImportDialog
          onClose={() => setImportOpen(false)}
          onImported={() => {
            void refreshLibrary()
            notifications.show({ message: 'AGY 账号已导入', color: 'green' })
          }}
        />
      )}
      {settingsOpen && (
        <AccountRefreshSettings
          platform="Antigravity"
          settings={settings}
          onChange={setSettings}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </div>
  )
}
