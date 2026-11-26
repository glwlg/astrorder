import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { Button, Checkbox, Group, TextInput } from '@mantine/core'
import type { AgyAccount } from './agyAccountsApi'

export type AccountPrompt =
  | { kind: 'save'; name: string }
  | { kind: 'switch'; account: AgyAccount }
  | { kind: 'delete'; account: AgyAccount }

export function AgyAccountDialog({ prompt, targetName, unsavedCurrent, onClose, onSave, onSwitch, onDelete }: {
  prompt: AccountPrompt
  targetName: string
  unsavedCurrent: boolean
  onClose: () => void
  onSave?: (name: string) => void
  onSwitch: (id: string, force: boolean) => void
  onDelete?: (id: string) => void
}) {
  const title = useId()
  const description = useId()
  const ref = useRef<HTMLDialogElement>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const cancelButton = useRef<HTMLButtonElement>(null)
  const [name, setName] = useState(prompt.kind === 'save' ? prompt.name : '')
  const [force, setForce] = useState(false)
  const reapply = prompt.kind === 'switch' && prompt.account.current

  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) {
      dialog.showModal()
      if (prompt.kind === 'save') nameInput.current?.focus()
      else cancelButton.current?.focus()
    }
  }, [prompt.kind])

  function close() {
    ref.current?.close()
    onClose()
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (prompt.kind === 'save' && name.trim()) onSave?.(name.trim())
    if (prompt.kind === 'switch') onSwitch(prompt.account.id, force)
    if (prompt.kind === 'delete') onDelete?.(prompt.account.id)
    close()
  }

  const heading = prompt.kind === 'save'
    ? '保存当前账号'
    : prompt.kind === 'delete'
      ? '删除已保存账号'
      : reapply
        ? '重新应用账号'
        : '切换账号'

  const submitLabel = prompt.kind === 'save'
    ? '保存'
    : prompt.kind === 'delete'
      ? '确认删除'
      : reapply
        ? '确认应用'
        : '确认切换'

  return (
    <dialog ref={ref} className="agy-account-dialog" aria-labelledby={title} aria-describedby={description}
      onCancel={event => { event.preventDefault(); close() }}>
      <form onSubmit={submit}>
        <h2 id={title}>{heading}</h2>
        {prompt.kind === 'save' ? <>
          <p id={description}>保存 {targetName} 当前登录的账号。</p>
          <TextInput ref={nameInput} label="账号名称" placeholder="例如 工作账号" value={name} maxLength={64} onChange={event => setName(event.currentTarget.value)} required />
        </> : prompt.kind === 'delete' ? <>
          <p id={description}>
            将从星序本地加密凭证库中删除「{prompt.account.name}」。不会修改目标机器上当前正在使用的登录文件。
          </p>
        </> : <>
          <p id={description}>
            {reapply ? `将「${prompt.account.name}」已保存的凭证重新应用到 ${targetName} 并重启。` : `将 ${targetName} 切换到「${prompt.account.name}」并重启。`}
            若仍有活动会话，默认会取消操作。
            {unsavedCurrent && ' 此目标当前的凭证尚未保存，切换将替换它。'}
          </p>
          <Checkbox label="强制重启并中断活动会话" checked={force} onChange={event => setForce(event.currentTarget.checked)} />
        </>}
        <Group justify="flex-end" gap="xs" mt="lg">
          <Button ref={cancelButton} type="button" variant="default" onClick={close}>取消</Button>
          <Button type="submit" color={prompt.kind === 'delete' ? 'red' : undefined} disabled={prompt.kind === 'save' && !name.trim()}>{submitLabel}</Button>
        </Group>
      </form>
    </dialog>
  )
}
