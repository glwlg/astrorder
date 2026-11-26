import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { Alert, Button, Group, Radio, Stack, Textarea, TextInput } from '@mantine/core'
import { accountPlatformMeta, accountsApiFor, agyImportMaxBytes, type AccountPlatform } from './agyAccountsApi'

export function AgyAccountImportDialog({ platform = 'antigravity', onClose, onImported }: {
  platform?: AccountPlatform
  onClose: () => void
  onImported: () => void
}) {
  const meta = accountPlatformMeta(platform)
  const api = accountsApiFor(platform)
  const title = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const pasteInput = useRef<HTMLTextAreaElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const [source, setSource] = useState('paste')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (dialog.current && !dialog.current.open) dialog.current.showModal()
    nameInput.current?.focus()
  }, [])

  function close() {
    dialog.current?.close()
    onClose()
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    const name = nameInput.current?.value.trim() ?? ''
    const file = fileInput.current?.files?.[0]
    const field = fileInput.current ?? pasteInput.current
    function reject(message: string) {
      field?.setCustomValidity(message)
      field?.reportValidity()
    }
    if (!name) {
      nameInput.current?.setCustomValidity('请填写账号名称')
      nameInput.current?.reportValidity()
      return
    }
    if (file && file.size > agyImportMaxBytes) {
      reject('请选择 1 MB 以内的 JSON 文件')
      return
    }
    let credential = pasteInput.current?.value ?? ''
    if (!file && (!credential.trim() || new TextEncoder().encode(credential).length > agyImportMaxBytes)) {
      reject(`请输入 1 MB 以内的 ${meta.credentialLabel}`)
      return
    }

    setError('')
    setPending(true)
    try {
      if (file) {
        try {
          credential = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(await file.arrayBuffer())
        } catch {
          setError('请选择 UTF-8 编码的 JSON 文件')
          return
        }
      }
      await api.importCredential(name, credential)
      close()
      onImported()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '导入失败，请重试')
    } finally {
      setPending(false)
    }
  }

  return <dialog ref={dialog} className="agy-account-dialog" aria-labelledby={title}
    onCancel={event => {
      event.preventDefault()
      if (!pending) close()
    }}>
    <form method="post" action={`/api/v1${api.importPath}`} aria-labelledby={title} onSubmit={submit} aria-busy={pending}>
      <h2 id={title}>导入 {meta.shortLabel} 账号</h2>
      <Stack gap="md">
        <TextInput ref={nameInput} name="name" label="账号名称" required maxLength={64} readOnly={pending}
          onChange={event => event.currentTarget.setCustomValidity('')} />
        <Radio.Group name="source" label="凭证来源" value={source} onChange={value => { setSource(value); setError('') }}>
          <Group mt="xs">
            <Radio value="paste" label="粘贴 JSON" disabled={pending} />
            <Radio value="file" label="上传文件" disabled={pending} />
          </Group>
        </Radio.Group>
        {source === 'paste' ? <Textarea ref={pasteInput} name="credential" label={meta.credentialLabel} required minRows={5}
          className="agy-credential-input" autoComplete="off" autoCorrect="off" autoCapitalize="none" spellCheck={false}
          readOnly={pending} onChange={event => { event.currentTarget.setCustomValidity(''); setError('') }} /> :
          <label className="agy-credential-file">{meta.fileLabel}
            <input ref={fileInput} type="file" name="credential_file" accept=".json,application/json" required disabled={pending}
              onChange={event => { event.currentTarget.setCustomValidity(''); setError('') }} />
          </label>}
        {error && <Alert color="red" role="alert" title="导入失败">{error}</Alert>}
      </Stack>
      <Group justify="flex-end" gap="xs" mt="lg">
        <Button type="button" variant="default" disabled={pending} onClick={close}>取消</Button>
        <Button type="submit" loading={pending}>导入账号</Button>
      </Group>
    </form>
  </dialog>
}
