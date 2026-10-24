import { useEffect, useRef, useState } from 'react'
import { Button, Group, Paper, Popover, Stack, Text } from '@mantine/core'
import { IconArchive, IconArchiveFilled, IconArchiveOff } from '@tabler/icons-react'
import type { DraftState } from '../../domain/types'
import { newCommandId } from '../../domain/semantics'
import { ApiError } from '../../api/client'
import { draftShelfStorage, draftStorage, type StashedDraft } from './draftStorage'

const emptyDraft = (): DraftState => ({ text: '', attachments: [], sessionRefs: [] })
const hasContent = (draft: DraftState) => Boolean(draft.text || draft.attachments.length || draft.sessionRefs?.length)
const entry = (draft: DraftState): StashedDraft => ({ id: newCommandId(), savedAt: new Date().toISOString(), draft })

export function DraftShelfControl({ scope, draft, disabled, onReplace, onError }: {
  scope: string
  draft: DraftState
  disabled: boolean
  onReplace: (expected: DraftState, replacement: DraftState) => boolean
  onError: (message: string) => void
}) {
  const [entries, setEntries] = useState<StashedDraft[]>([])
  const [ready, setReady] = useState(false)
  const [busy, setBusy] = useState(false)
  const [opened, setOpened] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [clearing, setClearing] = useState(false)
  const locked = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    let active = true
    void draftShelfStorage.load(scope).then(rows => {
      if (active) { setEntries(rows); setReady(true) }
    }).catch(error => { if (active) onError(`无法读取暂存草稿：${error instanceof Error ? error.message : '数据库不可用'}`) })
    return () => { active = false; mounted.current = false }
  }, [scope])

  const save = async (next: StashedDraft[], replace?: DraftState, restoredId?: string) => {
    if (locked.current || disabled || !ready) return
    locked.current = true
    setBusy(true)
    try {
      await draftShelfStorage.save(scope, next)
      if (!mounted.current) return
      setEntries(next)
      if (replace && !onReplace(draft, replace)) {
        onError('输入内容已变化，暂存已保存，请再次恢复草稿。')
      } else if (replace) {
        if (restoredId) {
          // Retain the source until the restored draft and displaced input are durable.
          await draftStorage.save(scope, replace)
          const remaining = next.filter(item => item.id !== restoredId)
          await draftShelfStorage.save(scope, remaining)
          if (mounted.current) setEntries(remaining)
        }
        setOpened(false)
      }
      setDeleting(null)
      setClearing(false)
    } catch (error) {
      if (mounted.current) onError(error instanceof Error ? error.message : '草稿暂存失败，输入内容已保留。')
      if (error instanceof ApiError && error.status === 409) {
        try {
          const rows = await draftShelfStorage.load(scope)
          if (mounted.current) setEntries(rows)
        } catch { /* Keep the input and the existing list if reload fails. */ }
      }
    } finally {
      locked.current = false
      if (mounted.current) setBusy(false)
    }
  }

  const restore = (row: StashedDraft) => void save(hasContent(draft) ? [entry(draft), ...entries] : entries, {
    ...row.draft, attachments: row.draft.attachments.map(item => ({ ...item, key: newCommandId() })),
  }, row.id)

  return <Popover opened={opened} onChange={setOpened} position="top-start" width="min(440px, calc(100vw - 32px))" shadow="md" withinPortal>
    <Popover.Target>
      <Button className="attachment-button" variant="subtle" color="gray" aria-label="草稿箱" title={hasContent(draft) ? '左键暂存当前内容，右键打开草稿箱' : '打开草稿箱'} aria-expanded={opened} disabled={disabled || busy || !ready}
        onClick={() => hasContent(draft) ? void save([entry(draft), ...entries], emptyDraft()) : setOpened(value => !value)}
        onContextMenu={event => { event.preventDefault(); if (!disabled && !busy && ready) setOpened(true) }}>
        {entries.length > 0 ? <IconArchiveFilled size={19} /> : <IconArchive size={19} />}
      </Button>
    </Popover.Target>
    <Popover.Dropdown className="composer-draft-shelf" p="xs" role="region" aria-label="暂存草稿列表" style={{ maxHeight: 'min(320px, 50dvh)', overflowY: 'auto' }}>
      <Stack gap="xs">
        <Text size="xs" c="dimmed">已保存到数据库，双击内容可移回输入框。</Text>
        {entries.length > 0 && <Group justify="flex-end" gap={4}>
          {clearing ? <>
            <Button size="compact-xs" variant="default" disabled={disabled || busy} onClick={() => void save([])}>确认清空</Button>
            <Button size="compact-xs" variant="subtle" color="gray" aria-label="取消清空" disabled={busy} onClick={() => setClearing(false)}>取消</Button>
          </> : <Button size="compact-xs" variant="subtle" color="gray" disabled={disabled || busy} onClick={() => { setDeleting(null); setClearing(true) }}>清空草稿箱</Button>}
        </Group>}
        {entries.length === 0 && <Text size="sm" c="dimmed">草稿箱为空</Text>}
        {entries.map(row => <Paper key={row.id} withBorder radius="sm" p="xs">
          <Text size="sm" lineClamp={2} onDoubleClick={() => restore(row)} title="双击恢复草稿" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', cursor: 'pointer' }}>{row.draft.text || row.draft.attachments.map(item => item.file.name).join('、') || '会话引用'}</Text>
          <Group justify="space-between" gap="xs" mt={4}>
            <Text size="xs" c="dimmed">{new Date(row.savedAt).toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}{row.draft.attachments.length ? ` · ${row.draft.attachments.length} 个附件` : ''}</Text>
            <Group gap={4}>
              <Button size="compact-xs" variant="default" aria-label="恢复草稿" disabled={disabled || busy} onClick={() => restore(row)}>恢复</Button>
              {deleting === row.id ? <>
                <Button size="compact-xs" variant="default" disabled={busy} onClick={() => void save(entries.filter(item => item.id !== row.id))}>确认删除</Button>
                <Button size="compact-xs" variant="subtle" color="gray" disabled={busy} onClick={() => setDeleting(null)}>取消</Button>
              </> : <Button size="compact-xs" variant="subtle" color="gray" aria-label="删除暂存草稿" disabled={busy} onClick={() => { setClearing(false); setDeleting(row.id) }}><IconArchiveOff size={14} /></Button>}
            </Group>
          </Group>
        </Paper>)}
      </Stack>
    </Popover.Dropdown>
  </Popover>
}
