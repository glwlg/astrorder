import { useEffect, useState } from 'react'
import { ActionIcon, Alert, Button, Loader } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery } from '@tanstack/react-query'
import { IconDeviceFloppy, IconPlus, IconRefresh, IconSettings, IconSwitchHorizontal, IconTrash } from '@tabler/icons-react'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { accountPlatformMeta, supportedAccountPlatforms, type AccountPlatform } from '../agentAccounts/agyAccountsApi'
import { AgyAccountDialog, type AccountPrompt } from '../agentAccounts/AgyAccountDialog'
import { AgyAccountImportDialog } from '../agentAccounts/AgyAccountImportDialog'
import { AgyQuotaTiles } from '../agentAccounts/AgyQuotaTiles'
import { agyStamp } from '../agentAccounts/agyQuotaFormat'
import { AccountRefreshSettings } from '../agentAccounts/AccountRefreshSettings'
import { useRefreshSettings } from '../agentAccounts/accountRefresh'
import { useAccountPageRefreshTarget } from '../agentAccounts/AgyQuotaRefreshProvider'
import { metadataPolicy, platformTargetsOptions, useAgyAccountOverview } from '../agentAccounts/useAgyAccountOverview'
import { useAgyAccountMutation } from '../agentAccounts/useAgyAccountMutation'
import '../agentAccounts/AgyAccountsPage.css'

export function MobileAccountsPanel({
  preferredPlatform = 'antigravity',
  preferredEnvironment = '',
}: {
  preferredPlatform?: AccountPlatform
  preferredEnvironment?: string
}) {
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const isAgentEnabled = usePluginSettingsStore((s) => s.isAgentEnabled)
  void enabledPlugins
  const availablePlatforms = supportedAccountPlatforms.filter((p) => isAgentEnabled(p))
  const fallbackPlatform = availablePlatforms.includes(preferredPlatform)
    ? preferredPlatform
    : (availablePlatforms[0] ?? 'antigravity')

  const [manualPlatform, setManualPlatform] = useState<AccountPlatform | null>(null)
  const platform = manualPlatform && availablePlatforms.includes(manualPlatform) ? manualPlatform : fallbackPlatform
  const meta = accountPlatformMeta(platform)

  const [manualEnvironment, setManualEnvironment] = useState<string | null>(null)
  const [dialog, setDialog] = useState<{ prompt: AccountPrompt; environment: string; platform: AccountPlatform } | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useRefreshSettings(platform)

  const targets = useQuery({ ...metadataPolicy, ...platformTargetsOptions(platform) })
  const targetList = targets.data?.items ?? []
  const envPref = platform === preferredPlatform ? preferredEnvironment : ''
  const target = (manualEnvironment ? targetList.find(item => item.id === manualEnvironment) : undefined)
    ?? (envPref ? targetList.find(item => item.id === envPref) : undefined)
    ?? targetList[0]
  const environment = target?.id ?? ''

  const { accounts, state: targetState, currentID, rows, quotas, refreshing, refresh, refreshLibrary } = useAgyAccountOverview(environment, platform)
  const quotaSupported = targetState.data?.quota_supported
  const prompt = dialog?.environment === environment && dialog?.platform === platform ? dialog.prompt : null

  useEffect(() => {
    if (environment && targetState.fetchStatus === 'idle' && !targetState.data && !targetState.isError) {
      void targetState.refetch()
    }
  }, [environment, platform])

  const mutation = useAgyAccountMutation(environment, platform)
  useAccountPageRefreshTarget(environment, Boolean(dialog || importOpen || settingsOpen), platform)

  async function openAction(account?: typeof rows[number]) {
    const latest = await targetState.refetch({ cancelRefetch: false })
    if (latest.isError || !latest.data) return
    const active = latest.data.current_account_id
    if (account) {
      setDialog({ platform, environment, prompt: { kind: 'switch', account: { ...account, current: active === account.id } } })
    } else if (active) {
      setDialog({ platform, environment, prompt: { kind: 'save', name: rows.find(row => row.id === active && active !== 'current')?.name ?? '' } })
    } else {
      notifications.show({ message: meta.notLoggedInHint })
    }
  }

  return (
    <div className="m-accounts-panel">
      <div className="m-accounts-toolbar">
        {availablePlatforms.length > 1 && (
          <div className="m-accounts-platforms" role="tablist" aria-label="账号平台">
            {availablePlatforms.map((item) => {
              const itemMeta = accountPlatformMeta(item)
              const selected = item === platform
              return (
                <button
                  key={item}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className="m-accounts-platform-tab"
                  data-active={selected || undefined}
                  disabled={mutation.isPending}
                  onClick={() => {
                    mutation.reset()
                    setManualPlatform(item)
                    setManualEnvironment(null)
                    setDialog(null)
                  }}
                >
                  <AgentBrandIcon kind={item} size={15} />
                  <span>{itemMeta.label}</span>
                </button>
              )
            })}
          </div>
        )}

        <div className="m-accounts-target-row">
          <label className="m-accounts-select">
            <span>
              <AgentBrandIcon kind={platform} size={14} /> {meta.label} 目标机器
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
              {!targetList.length && <option value="">暂无可用 {meta.shortLabel} 目标</option>}
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
            {mutation.variables?.kind === 'save'
              ? '正在保存当前账号…'
              : mutation.variables?.kind === 'delete'
                ? '正在删除账号…'
                : `正在切换账号并重新接入 ${meta.shortLabel}…`}
          </Alert>
        )}
        {accounts.isPending && (
          <div className="m-accounts-empty" role="status">
            <Loader size="xs" color="violet" /> 正在读取账号…
          </div>
        )}
        {targets.isSuccess && !target && (
          <Alert>
            {targetList.length ? '所选目标不可用，请重新选择目标 Agent。' : meta.targetEmptyHint}
          </Alert>
        )}
        {accounts.isSuccess && !rows.length && (
          <div className="m-accounts-empty">
            暂无已保存账号，可点击上方「导入账号」或「保存当前账号」。
          </div>
        )}

        {accounts.isSuccess && rows.length > 0 && (
          <div className="m-accounts-cards" role="list" aria-label={`${meta.shortLabel} 账号列表`}>
            {rows.map((account, index) => {
              const quota = quotas[index]
              const plan = quota.data?.plan || quota.data?.tier_id
              const subLabel = quota.data?.account_label || account.email
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
                      {subLabel && subLabel !== account.name && (
                        <span className="m-account-sublabel">{subLabel}</span>
                      )}
                      <div className="m-account-card-badges">
                        {account.current && <span className="agy-current-badge">当前使用</span>}
                        {plan && (
                          <span className="m-account-tier" title={plan}>
                            {plan.replace(/^(Google AI |ChatGPT )/i, '')}
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
                    {meta.canDelete && account.id !== 'current' && (
                      <ActionIcon
                        size="sm"
                        variant="subtle"
                        color="red"
                        aria-label={`删除 ${account.name}`}
                        disabled={mutation.isPending || refreshing}
                        onClick={() => setDialog({ platform, environment, prompt: { kind: 'delete', account } })}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    )}
                  </footer>
                </article>
              )
            })}
          </div>
        )}
      </div>

      {prompt && (target || prompt.kind === 'delete') && (
        <AgyAccountDialog
          prompt={prompt}
          targetName={target?.name ?? meta.label}
          unsavedCurrent={currentID === 'current'}
          onClose={() => setDialog(null)}
          onSave={name => mutation.mutate({ kind: 'save', name })}
          onSwitch={(id, force) => mutation.mutate({ kind: 'switch', id, force })}
          onDelete={id => mutation.mutate({ kind: 'delete', id })}
        />
      )}
      {importOpen && (
        <AgyAccountImportDialog
          platform={platform}
          onClose={() => setImportOpen(false)}
          onImported={() => {
            void refreshLibrary()
            notifications.show({ message: `${meta.shortLabel} 账号已导入`, color: 'green' })
          }}
        />
      )}
      {settingsOpen && (
        <AccountRefreshSettings
          platform={meta.label}
          settings={settings}
          onChange={setSettings}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </div>
  )
}
