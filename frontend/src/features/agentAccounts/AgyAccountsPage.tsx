import { useState } from 'react'
import { Alert, Button, Loader, NativeSelect, Table } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery } from '@tanstack/react-query'
import { IconDeviceFloppy, IconPlus, IconRefresh, IconSettings, IconSwitchHorizontal } from '@tabler/icons-react'
import { agyAccountsApi } from './agyAccountsApi'
import { AgyAccountDialog, type AccountPrompt } from './AgyAccountDialog'
import { AgyAccountImportDialog } from './AgyAccountImportDialog'
import { AgyQuotaTiles } from './AgyQuotaTiles'
import { agyStamp } from './agyQuotaFormat'
import { AccountRefreshSettings } from './AccountRefreshSettings'
import { useRefreshSettings, useScheduledRefresh } from './accountRefresh'
import { metadataPolicy, useAgyAccountOverview } from './useAgyAccountOverview'
import { useAgyAccountMutation } from './useAgyAccountMutation'
import './AgyAccountsPage.css'

export function AgyAccountsPage() {
  const [selection, setSelection] = useState('')
  const [dialog, setDialog] = useState<{ prompt: AccountPrompt; environment: string } | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useRefreshSettings('antigravity')
  const targets = useQuery({ ...metadataPolicy, queryKey: ['agy-account-targets'], queryFn: agyAccountsApi.targets })
  const target = targets.data?.items.find(item => item.id === selection) ?? targets.data?.items[0]
  const environment = target?.id ?? ''
  const { accounts, state: targetState, currentID, rows, quotas, refreshing, refresh, refreshLibrary } = useAgyAccountOverview(environment)
  const quotaSupported = targetState.data?.quota_supported
  const prompt = dialog?.environment === environment ? dialog.prompt : null

  const mutation = useAgyAccountMutation(environment)
  useScheduledRefresh(environment, settings, mode => {
    if (!mutation.isPending && !dialog && !importOpen && !refreshing) void refresh(mode)
  })

  async function openAction(account?: typeof rows[number]) {
    const latest = await targetState.refetch({ cancelRefetch: false })
    if (latest.isError || !latest.data) return
    const active = latest.data.current_account_id
    if (account) setDialog({ environment, prompt: { kind: 'switch', account: { ...account, current: active === account.id } } })
    else if (active) setDialog({ environment, prompt: { kind: 'save', name: rows.find(row => row.id === active && active !== 'current')?.name ?? '' } })
    else notifications.show({ message: '目标 Agent 尚未登录 AGY' })
  }

  return <section className="agy-accounts-page">
    <header className="agy-accounts-toolbar">
      <div><h2>Antigravity</h2><span className="agy-account-muted">{accounts.data ? `${accounts.data.items.length} 个已保存账号` : '账号库'}</span></div>
      <div className="agy-accounts-tools">
        {target && <div className="agy-target-select">
          <label htmlFor="agy-account-target">目标 Agent</label>
          <NativeSelect id="agy-account-target" value={environment} onChange={event => {
            mutation.reset()
            setSelection(event.currentTarget.value)
          }}
            data={targets.data?.items.map(({ id, name }) => ({ value: id, label: name }))} disabled={mutation.isPending} />
        </div>}
        <Button variant="default" leftSection={<IconRefresh size={16} />} onClick={() => { void targets.refetch(); if (target) void refresh('all'); else void accounts.refetch() }}
          loading={refreshing} disabled={mutation.isPending || accounts.isPending}>刷新全部</Button>
        <Button variant="default" leftSection={<IconDeviceFloppy size={16} />} onClick={() => void openAction()}
          disabled={!target || refreshing || mutation.isPending}>保存当前账号</Button>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setImportOpen(true)} disabled={mutation.isPending}>导入账号</Button>
        <Button variant="subtle" leftSection={<IconSettings size={16} />} onClick={() => setSettingsOpen(true)}>刷新设置</Button>
      </div>
    </header>
    {targets.isError && <Alert color="red" title="无法读取 Agent">{targets.error.message} <Button variant="subtle" size="compact-sm" onClick={() => void targets.refetch()}>重试</Button></Alert>}
    {accounts.isError && <Alert color="red" title="无法读取账号">{accounts.error.message} <Button variant="subtle" size="compact-sm" onClick={() => void accounts.refetch()}>重试</Button></Alert>}
    {targetState.isError && <Alert color="red" title="无法读取目标 Agent 的账号状态">{targetState.error.message} <Button variant="subtle" size="compact-sm" onClick={() => void targetState.refetch()}>重试</Button></Alert>}
    {mutation.isError && <Alert color="red" title="账号操作未完成">{mutation.error.message}</Alert>}
    {mutation.isPending && <Alert role="status">{mutation.variables?.kind === 'save' ? '正在保存当前账号…' : '正在切换账号并重新接入 AGY…'}</Alert>}
    {accounts.isPending && <div className="agy-account-empty" role="status"><Loader size="xs" /> 正在读取账号</div>}
    {targets.isSuccess && !target && <Alert>接入 AGY Agent 后即可应用账号和查询额度。</Alert>}
    {accounts.isSuccess && !rows.length && <div className="agy-account-empty">暂无已保存账号，可导入 AGY Token JSON 或保存当前账号。</div>}
    {accounts.isSuccess && rows.length > 0 && <div className="agy-account-table-wrap">
      <Table className="agy-account-table" aria-label="AGY 账号额度总览" verticalSpacing="md" horizontalSpacing="md">
        <colgroup><col className="agy-account-name-col" /><col className="agy-plan-col" /><col /><col className="agy-account-time-col" /><col className="agy-account-actions-col" /></colgroup>
        <Table.Thead><Table.Tr>
          <Table.Th scope="col">账号</Table.Th><Table.Th scope="col">订阅档位</Table.Th><Table.Th scope="col">剩余额度与重置</Table.Th>
          <Table.Th scope="col">更新于</Table.Th><Table.Th scope="col">操作</Table.Th>
        </Table.Tr></Table.Thead>
        <Table.Tbody>{rows.map((account, index) => {
          const quota = quotas[index]
          return <Table.Tr key={account.id} data-current={account.current || undefined}>
            <Table.Th scope="row">
              <span className="agy-account-name">{account.name}</span>
              {account.current && <span className="agy-current-badge">当前使用</span>}
            </Table.Th>
            <Table.Td><span className="agy-plan" data-known={Boolean(quota.data?.plan || quota.data?.tier_id)}>{quota.data?.plan || quota.data?.tier_id || (quota.data ? '未返回' : '—')}</span></Table.Td>
            <Table.Td aria-busy={quota.isFetching}>
              {quota.isError && <div className="agy-account-error" role="status"><strong>额度查询失败</strong><span>{quota.error.message}</span></div>}
              {quota.data ? <AgyQuotaTiles quota={quota.data} /> : !quota.isError &&
                <span className="agy-account-muted">{!target ? '请选择目标 Agent' : quotaSupported === false ? '此系统暂不支持隔离查询' : quota.isFetching ? '正在读取额度…' : '尚未刷新'}</span>}
            </Table.Td>
            <Table.Td className="agy-account-timestamp">
              {quota.data ? <time dateTime={quota.data.as_of}>{agyStamp(quota.data.as_of)}</time> : '—'}
            </Table.Td>
            <Table.Td><div className="agy-row-actions">
              {target && quotaSupported !== false && <Button size="compact-sm" variant="default" leftSection={<IconRefresh size={14} />} aria-label={`刷新 ${account.name} 的额度`}
                loading={quota.isFetching} disabled={mutation.isPending || refreshing} onClick={() => void refresh({ id: account.id })}>刷新</Button>}
              {account.id !== 'current' && target && <Button size="compact-sm" variant="light" leftSection={<IconSwitchHorizontal size={14} />}
                aria-label={account.current ? `重新应用 ${account.name}` : `切换到 ${account.name}`}
                disabled={mutation.isPending || refreshing} onClick={() => void openAction(account)}>
                {account.current ? '重新应用' : '切换'}
              </Button>}
            </div></Table.Td>
          </Table.Tr>
        })}</Table.Tbody>
      </Table>
    </div>}
    {prompt && target && <AgyAccountDialog prompt={prompt} targetName={target.name} unsavedCurrent={currentID === 'current'} onClose={() => setDialog(null)}
      onSave={name => mutation.mutate({ kind: 'save', name })} onSwitch={(id, force) => mutation.mutate({ kind: 'switch', id, force })} />}
    {importOpen && <AgyAccountImportDialog onClose={() => setImportOpen(false)} onImported={() => {
      void refreshLibrary()
      notifications.show({ message: 'AGY 账号已导入', color: 'green' })
    }} />}
    {settingsOpen && <AccountRefreshSettings platform="Antigravity" settings={settings} onChange={setSettings} onClose={() => setSettingsOpen(false)} />}
  </section>
}
