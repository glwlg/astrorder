import { afterEach, describe, expect, it, onTestFinished, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { api, type BackupJob, type BackupTarget, type BackupVersion } from '../../api/client'
import { BackupsPage } from './BackupsPage'

const targetsFixture: { items: BackupTarget[]; default_destination_dir: string } = {
  default_destination_dir: 'D:\\AstrorderBackups',
  items: [
    { id: 'local', connection_id: 'local', kind: 'local', name: '本机', state: 'connected' },
    { id: 'ssh-debian', connection_id: 'ssh-debian', kind: 'ssh', name: 'DebianProd', state: 'connected', workspace: '/srv/app' },
  ],
}

const jobFixture: BackupJob = {
  id: 'bkj-1',
  name: '核心工程配置',
  connection_id: 'ssh-debian',
  source_path: '/home/luwei/projects/demo',
  destination_dir: 'D:\\AstrorderBackups',
  post_restore_command: '',
  schedule_enabled: true,
  schedule_mode: 'daily',
  interval_minutes: 60,
  daily_time: '03:00',
  weekday: 1,
  retention_count: 20,
  last_run_at: '2026-11-20T02:00:00.000Z',
  last_status: 'success',
  last_error: null,
  next_run_at: '2026-11-21T19:00:00.000Z',
  created_at: '2026-11-20T01:00:00.000Z',
  updated_at: '2026-11-20T02:00:00.000Z',
  target_name: 'DebianProd',
  target_kind: 'ssh',
  target_state: 'connected',
  version_count: 1,
  latest_version: null,
  is_running: false,
}

const versionFixture: BackupVersion = {
  id: 'bkv-1',
  job_id: 'bkj-1',
  version_number: 1,
  version_label: 'v1',
  trigger_kind: 'manual',
  note: '上线前快照',
  status: 'success',
  error_detail: null,
  local_path: 'D:\\AstrorderBackups\\核心工程配置_bkj-1\\v001_20261120-100000',
  source_kind: 'directory',
  file_count: 12,
  total_bytes: 20480,
  manifest_sha256: 'abc123',
  created_at: '2026-11-20T02:00:00.000Z',
  finished_at: '2026-11-20T02:00:05.000Z',
  exists_on_disk: true,
}

function renderBackups() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <BackupsPage />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('BackupsPage', () => {
  it('renders jobs and versions, edits version note, opens folder, triggers manual backup with note, creates job, and deletes version', async () => {
    vi.spyOn(api, 'listBackupTargets').mockResolvedValue(targetsFixture)
    vi.spyOn(api, 'listBackupJobs').mockResolvedValue({ items: [jobFixture] })
    vi.spyOn(api, 'listBackupVersions').mockResolvedValue({ items: [versionFixture] })
    const runSpy = vi.spyOn(api, 'runBackupJob').mockResolvedValue({
      version: { ...versionFixture, id: 'bkv-2', version_number: 2, version_label: 'v2', note: '重构前留存', status: 'running' },
    })
    const noteSpy = vi.spyOn(api, 'updateBackupVersionNote').mockResolvedValue({
      ...versionFixture,
      note: '已确认回滚基线',
    })
    const openSpy = vi.spyOn(api, 'openBackupVersionFolder').mockResolvedValue({
      opened: true,
      path: versionFixture.local_path,
    })
    const createSpy = vi.spyOn(api, 'createBackupJob').mockResolvedValue({
      ...jobFixture,
      id: 'bkj-2',
      name: '新备份任务',
    })
    const deleteVersionSpy = vi.spyOn(api, 'deleteBackupVersion').mockResolvedValue({ deleted: true })

    renderBackups()

    expect(await screen.findByText('文件与目录备份')).toBeInTheDocument()
    const detailPane = screen.getByRole('region', { name: '版本历史详情' })
    expect(await within(detailPane).findByText('核心工程配置')).toBeInTheDocument()
    expect(await within(detailPane).findByText('v1')).toBeInTheDocument()
    expect(await within(detailPane).findByText(/上线前快照/)).toBeInTheDocument()

    // 1. Trigger manual backup with note
    fireEvent.click(screen.getByRole('button', { name: '立即备份 核心工程配置' }))
    const runDialog = await screen.findByRole('dialog', { name: /手动备份/ })
    const noteArea = within(runDialog).getByLabelText('本次版本备注（可选）')
    fireEvent.change(noteArea, { target: { value: '重构前留存' } })
    fireEvent.click(within(runDialog).getByRole('button', { name: '开始备份' }))
    await waitFor(() => expect(runSpy).toHaveBeenCalledWith('bkj-1', '重构前留存'))

    // 2. Inline edit version note
    fireEvent.click(screen.getByRole('button', { name: '编辑备注' }))
    const noteInput = screen.getByLabelText('编辑版本备注')
    fireEvent.change(noteInput, { target: { value: '已确认回滚基线' } })
    fireEvent.click(screen.getByRole('button', { name: '保存备注' }))
    await waitFor(() => expect(noteSpy).toHaveBeenCalledWith('bkv-1', '已确认回滚基线'))

    // 3. Open local folder
    fireEvent.click(screen.getByRole('button', { name: '打开目录' }))
    await waitFor(() => expect(openSpy).toHaveBeenCalledWith('bkv-1'))

    // 4. Create new backup job
    fireEvent.click(screen.getByRole('button', { name: '新建备份任务' }))
    const createDialog = await screen.findByRole('dialog', { name: '新建备份任务' })
    fireEvent.change(within(createDialog).getByLabelText('任务名称'), { target: { value: '新备份任务' } })
    const sourceInput = within(createDialog).getByLabelText(/源目录或文件路径/)
    expect(sourceInput).toHaveAccessibleDescription('输入源服务器上的绝对路径，或点击下方按钮浏览。')
    fireEvent.change(sourceInput, { target: { value: 'C:\\Users\\luwei\\.ssh' } })
    fireEvent.click(within(createDialog).getByRole('radio', { name: '自定义命令' }))
    const hook = within(createDialog).getByLabelText('还原后命令（可选）')
    expect(hook).toHaveAccessibleDescription(/Windows 本机使用 PowerShell/)
    fireEvent.change(hook, { target: { value: 'Write-Output restored' } })
    fireEvent.click(within(createDialog).getByRole('button', { name: '创建任务' }))
    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          name: '新备份任务',
          source_path: 'C:\\Users\\luwei\\.ssh',
          destination_dir: 'D:\\AstrorderBackups',
          post_restore_command: 'Write-Output restored',
        }),
      ),
    )

    // 5. Delete version with local files checked
    fireEvent.click(screen.getByRole('button', { name: '删除' }))
    const delDialog = await screen.findByRole('dialog', { name: '确认删除备份版本' })
    fireEvent.click(within(delDialog).getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(deleteVersionSpy).toHaveBeenCalledWith('bkv-1', true))
  })

  it('saves a specific Agent on its selected environment as a restart preset', async () => {
    vi.spyOn(api, 'listBackupTargets').mockResolvedValue(targetsFixture)
    vi.spyOn(api, 'listBackupJobs').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getEnvironments').mockResolvedValue({
      items: [
        { id: 'local', name: '本机', method: 'local', discovered: true, agents: [{ kind: 'codex', available: true, state: 'connected', detail: '' }] },
        { id: 'ssh-debian', name: 'DebianProd', method: 'ssh', discovered: true, agents: [{ kind: 'codex', available: true, state: 'connected', detail: '' }] },
      ],
    })
    const save = vi.spyOn(api, 'createBackupJob').mockResolvedValue(jobFixture)
    renderBackups()
    const open = await screen.findAllByRole('button', { name: '新建备份任务' })
    fireEvent.click(open[0])
    const dialog = within(await screen.findByRole('dialog', { name: '新建备份任务' }))
    fireEvent.change(dialog.getByLabelText(/源目录或文件路径/), { target: { value: 'C:\\Users\\luwei\\.ssh' } })
    fireEvent.click(dialog.getByRole('radio', { name: '重启 Agent' }))
    const target = dialog.getByRole('combobox', { name: '要重启的 Agent' })
    const agent = await dialog.findByRole('option', { name: 'DebianProd · Codex' }) as HTMLOptionElement
    fireEvent.click(dialog.getByRole('button', { name: '创建任务' }))
    expect(save).not.toHaveBeenCalled()
    expect(target).toHaveFocus()
    fireEvent.change(target, { target: { value: agent.value } })
    fireEvent.click(dialog.getByRole('button', { name: '创建任务' }))
    await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      post_restore_command: '',
      post_restore_restart_agent: { environment_id: 'ssh-debian', kind: 'codex' },
    })))
  })

  it('confirms the historical target and hook, and reports a hook failure separately', async () => {
    vi.spyOn(api, 'listBackupTargets').mockResolvedValue(targetsFixture)
    vi.spyOn(api, 'listBackupJobs').mockResolvedValue({ items: [jobFixture] })
    vi.spyOn(api, 'listBackupVersions').mockResolvedValue({ items: [versionFixture] })
    vi.spyOn(api, 'getBackupRestorePlan').mockResolvedValue({
      version_id: versionFixture.id,
      connection_id: 'ssh-debian',
      target_path: '/srv/original',
      source_kind: 'directory',
      post_restore_command: 'exit 7',
      hook_shell: '/bin/sh',
      confirmation: 'verified-plan',
    })
    const restore = vi.spyOn(api, 'restoreBackupVersion').mockResolvedValue({
      file_count: 12,
      hook_status: 'failed',
      hook_error: '还原后命令退出，退出码 7。',
    })

    renderBackups()
    fireEvent.click(await screen.findByRole('button', { name: '还原' }))
    const dialog = await screen.findByRole('dialog', { name: '还原备份 · v1' })
    expect(await within(dialog).findByText('/srv/original')).toBeInTheDocument()
    expect(within(dialog).getByText('exit 7')).toBeInTheDocument()
    expect(restore).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: '确认还原' }))
    await waitFor(() => expect(restore).toHaveBeenCalledExactlyOnceWith('bkv-1', 'verified-plan'))
    expect(await within(dialog).findByText(/已还原 12 个文件。还原后命令退出，退出码 7。/)).toBeInTheDocument()
  })

  it('force restarts the selected Antigravity after a failed hook without repeating the restore', async () => {
    const showModal = vi.fn(function (this: HTMLDialogElement) {
      this.open = true
    })
    const original = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: showModal })
    onTestFinished(() => {
      if (original) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', original)
      else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
    })
    vi.spyOn(api, 'listBackupTargets').mockResolvedValue(targetsFixture)
    vi.spyOn(api, 'listBackupJobs').mockResolvedValue({ items: [jobFixture] })
    vi.spyOn(api, 'listBackupVersions').mockResolvedValue({ items: [versionFixture] })
    vi.spyOn(api, 'getBackupRestorePlan').mockResolvedValue({
      version_id: versionFixture.id, connection_id: 'ssh-debian', target_path: '/srv/app',
      source_kind: 'directory', post_restore_command: '', hook_shell: 'sh', confirmation: 'restore-token',
      post_restore_restart_agent: { environment_id: 'ssh-wsl', kind: 'antigravity' },
      restore_agent_environment_name: 'WSL',
    })
    const restore = vi.spyOn(api, 'restoreBackupVersion').mockResolvedValue({
      file_count: 12, hook_status: 'failed', hook_error: 'Agent 有会话正在运行',
    })
    const restart = vi.spyOn(api, 'restartEnvironmentAgent').mockResolvedValue({})
    renderBackups()
    fireEvent.click(await screen.findByRole('button', { name: '还原' }))
    const confirmRestore = await screen.findByRole('button', { name: '确认还原' })
    await waitFor(() => expect(confirmRestore).toBeEnabled())
    fireEvent.click(confirmRestore)
    const force = await screen.findByRole('button', { name: '强制重启 Antigravity' })
    fireEvent.click(force)
    let dialog = screen.getByRole('dialog', { name: '强制重启 Antigravity' }) as HTMLDialogElement
    expect(showModal).toHaveBeenCalled()
    expect(within(dialog).getByText(/WSL 中此 Agent/)).toBeInTheDocument()
    dialog.returnValue = 'cancel'
    fireEvent(dialog, new Event('close'))
    expect(restart).not.toHaveBeenCalled()
    fireEvent.click(force)
    dialog = screen.getByRole('dialog', { name: '强制重启 Antigravity' }) as HTMLDialogElement
    dialog.returnValue = 'restart'
    fireEvent(dialog, new Event('close'))
    await waitFor(() => expect(restart).toHaveBeenCalledExactlyOnceWith('ssh-wsl', 'antigravity', true))
    expect(restore).toHaveBeenCalledExactlyOnceWith('bkv-1', 'restore-token')
    expect(await screen.findByText(/Antigravity 已重启/)).toBeInTheDocument()
  })

  it('clones a task with its versions and confirms restore on the newly selected server', async () => {
    const copiedVersion = { ...versionFixture, id: 'bkv-clone', job_id: 'bkj-clone' }
    const copiedJob = {
      ...jobFixture,
      id: copiedVersion.job_id,
      name: `${jobFixture.name} 副本`,
      connection_id: 'local',
      target_name: '本机',
      target_kind: 'local',
      source_path: 'D:\\Migrated\\config',
      latest_version: copiedVersion,
    }
    let jobs = [jobFixture]
    vi.spyOn(api, 'listBackupTargets').mockResolvedValue(targetsFixture)
    vi.spyOn(api, 'listBackupJobs').mockImplementation(async () => ({ items: jobs }))
    vi.spyOn(api, 'listBackupVersions').mockImplementation(async (id) => ({
      items: [id === copiedJob.id ? copiedVersion : versionFixture],
    }))
    const clone = vi.spyOn(api, 'cloneBackupJob').mockImplementation(async () => {
      jobs = [jobFixture, copiedJob]
      return copiedJob
    })
    vi.spyOn(api, 'getBackupRestorePlan').mockResolvedValue({
      version_id: copiedVersion.id, connection_id: copiedJob.connection_id,
      target_path: copiedJob.source_path, source_kind: 'directory',
      post_restore_command: '', hook_shell: 'Windows PowerShell', confirmation: 'cloned-target',
    })
    const restore = vi.spyOn(api, 'restoreBackupVersion').mockResolvedValue({ file_count: 12, hook_status: 'skipped' })
    renderBackups()

    fireEvent.click(await screen.findByRole('button', { name: '克隆任务' }))
    const dialog = within(await screen.findByRole('dialog', { name: '克隆备份任务' }))
    expect(dialog.getByLabelText('任务名称')).toHaveValue(copiedJob.name)
    expect(dialog.getByLabelText('启用定时自动备份')).toBeChecked()
    fireEvent.change(dialog.getByLabelText('源服务器'), { target: { value: 'local' } })
    const path = dialog.getByLabelText(/源目录或文件路径/)
    expect(path).toHaveValue('')
    fireEvent.click(dialog.getByRole('button', { name: '克隆任务' }))
    expect(path).toHaveFocus()
    expect(clone).not.toHaveBeenCalled()
    fireEvent.change(path, { target: { value: copiedJob.source_path } })
    fireEvent.click(dialog.getByRole('checkbox', { name: '同时克隆成功的备份版本' }))
    fireEvent.click(dialog.getByRole('button', { name: '克隆任务' }))
    await waitFor(() => expect(clone).toHaveBeenCalledExactlyOnceWith(jobFixture.id, expect.objectContaining({
      name: copiedJob.name, source_path: copiedJob.source_path, connection_id: 'local',
      copy_versions: true, schedule_enabled: true, schedule_mode: 'daily', daily_time: '03:00',
    })))

    const details = within(screen.getByRole('region', { name: '版本历史详情' }))
    await details.findByText(copiedJob.name)
    fireEvent.click(await details.findByRole('button', { name: '还原' }))
    const confirm = within(await screen.findByRole('dialog', { name: '还原备份 · v1' }))
    expect(await confirm.findByText(copiedJob.source_path)).toBeInTheDocument()
    expect(restore).not.toHaveBeenCalled()
    fireEvent.click(confirm.getByRole('button', { name: '确认还原' }))
    await waitFor(() => expect(restore).toHaveBeenCalledExactlyOnceWith(copiedVersion.id, 'cloned-target'))
  })
})
