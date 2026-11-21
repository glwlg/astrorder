import { Button, NativeSelect, Stack } from '@mantine/core'
import { useEffect, useId, useRef } from 'react'
import { refreshMinutes, type RefreshSettings, type RefreshScope } from './accountRefresh'

export function AccountRefreshSettings({ platform, settings, onChange, onClose }: {
  platform: string; settings: RefreshSettings; onChange: (next: RefreshSettings) => void; onClose: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const title = useId()
  useEffect(() => { dialog.current?.showModal() }, [])
  function close() {
    dialog.current?.close()
    onClose()
  }
  const choices = refreshMinutes.map(minutes => ({ value: String(minutes), label: minutes ? `${minutes} 分钟` : '关闭' }))
  function interval(scope: RefreshScope, label: string) {
    return <NativeSelect label={label} value={settings[scope]} data={choices}
      onChange={event => onChange({ ...settings, [scope]: Number(event.currentTarget.value) })} />
  }
  return <dialog ref={dialog} aria-labelledby={title} className="agy-account-dialog"
    onCancel={event => { event.preventDefault(); close() }}>
    <Stack gap="md">
      <h2 id={title}>{platform} 刷新设置</h2>
      {interval('all', '全部账号刷新间隔')}
      {interval('current', '当前账号刷新间隔')}
      <p>查看 {platform} 账号页或会话时按间隔刷新。窗口隐藏期间暂停，返回时补刷已到期的额度。</p>
      <Button onClick={close}>完成</Button>
    </Stack>
  </dialog>
}
