import { useCallback, useState, type MouseEvent } from 'react'
import { ActionIcon, Alert, Badge, Button, Group, Loader, Modal, NumberInput, Stack, Text, Textarea, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconArrowBackUp, IconArrowUpLeft, IconCheck, IconMessagePlus, IconPointer, IconRefresh, IconRestore } from '@tabler/icons-react'
import { api, type BrowserSnapshot } from '../../../../api/client'
import { addDesignToDraft, cssColorToHex, designProperties, type DesignProperty } from './browserDesign'
import { useBrowserDesigner } from './useBrowserDesigner'
import './browserDesign.css'

interface Props {
  agentId: string
  sessionId: string
  targetId: string
  snapshot: BrowserSnapshot
  onSnapshot: (snapshot: BrowserSnapshot) => void
  onClose: () => void
}

const numericGroups: Array<{ title: string; fields: DesignProperty[] }> = [
  { title: '尺寸与文字', fields: ['width', 'height', 'font-size', 'border-radius', 'gap'] },
  { title: '内边距', fields: ['padding-top', 'padding-right', 'padding-bottom', 'padding-left'] },
  { title: '外边距', fields: ['margin-top', 'margin-right', 'margin-bottom', 'margin-left'] },
]

export function BrowserDesignPanel({ agentId, sessionId, targetId, snapshot: initialSnapshot, onSnapshot, onClose }: Props) {
  const [snapshot, setSnapshot] = useState(initialSnapshot)
  const updateSnapshot = useCallback((next: BrowserSnapshot) => {
    setSnapshot(next)
    onSnapshot(next)
  }, [onSnapshot])
  const { state, error, busy, staged, change, action } = useBrowserDesigner(agentId, sessionId, targetId, updateSnapshot)
  const [note, setNote] = useState('')
  const [adding, setAdding] = useState(false)
  const [added, setAdded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [localError, setLocalError] = useState('')
  const selected = state?.selected
  const values = selected ? { ...selected.styles, ...staged } : null
  const count = state?.changes.reduce((total, entry) => total + entry.properties.length, 0) || 0
  const source = `data:${snapshot.mime_type};base64,${snapshot.screenshot}`

  const select = (event: MouseEvent<HTMLButtonElement>) => {
    if (busy || !state) return
    const rect = event.currentTarget.getBoundingClientRect()
    void action({ action: 'select', ratio_x: (event.clientX - rect.left) / rect.width, ratio_y: (event.clientY - rect.top) / rect.height })
  }

  const addToChat = async () => {
    setAdding(true)
    setLocalError('')
    try {
      const response = await action({ action: 'export' })
      if (!response || (response.state.changes.length === 0 && !(response.state.selected && note.trim()))) return
      await addDesignToDraft(agentId, sessionId, response, note)
      setAdded(true)
      notifications.show({ message: '标注截图和说明已加入当前对话', color: 'teal' })
      onClose()
    } catch (value) {
      setLocalError(value instanceof Error ? value.message : '加入对话失败')
    } finally {
      setAdding(false)
    }
  }

  const refresh = async () => {
    setRefreshing(true)
    setLocalError('')
    try {
      const next = await api.navigateBrowser(agentId, sessionId, snapshot.url, targetId)
      updateSnapshot(next)
      await action({ action: 'state' })
    } catch (value) {
      setLocalError(value instanceof Error ? value.message : '刷新预览失败')
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <Modal
      opened onClose={() => { if (!adding) void action({ action: 'state' }).then(result => { if (result) onClose() }) }}
      title="调整界面" size="calc(100vw - 32px)" centered
      classNames={{ content: 'browser-design-modal', body: 'browser-design-modal-body', header: 'browser-design-modal-header' }}
    >
      <div className="browser-design">
        <div className="browser-design-toolbar">
          <Group gap={8}>
            <Badge variant="light" color="violet" leftSection={<IconPointer size={12} />}>点选区域</Badge>
            <span className="browser-design-url" title={snapshot.url}>{snapshot.url}</span>
          </Group>
          <Group gap={6} wrap="nowrap">
            <span className="browser-design-status" role="status">{busy ? <><Loader size={12} />更新预览</> : count ? `${count} 项调整` : '预览已连接'}</span>
            <Tooltip label="撤销上一步">
              <ActionIcon variant="subtle" size={30} aria-label="撤销上一步" disabled={busy || !state?.can_undo} onClick={() => void action({ action: 'undo' })}>
                <IconArrowBackUp size={17} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="还原全部调整">
              <ActionIcon variant="subtle" size={30} aria-label="还原全部调整" disabled={busy || !count} onClick={() => void action({ action: 'reset' })}>
                <IconRestore size={17} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="重新加载页面，清除临时调整">
              <ActionIcon variant="subtle" size={30} aria-label="重新加载页面" loading={refreshing} disabled={busy} onClick={() => void refresh()}>
                <IconRefresh size={17} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </div>
        {(error || localError) && <Alert color="red" role="alert" m="xs">{localError || error}</Alert>}
        <div className="browser-design-layout">
          <div className="browser-design-stage" role="region" aria-label="页面预览" tabIndex={0}>
            <button
              type="button" className="browser-design-canvas" onClick={select} disabled={!state || busy}
              style={{ maxWidth: state?.viewport.width }}
              aria-label="在预览中点选要调整的元素"
            >
              <img src={source} alt="当前页面预览" draggable={false} />
              {selected && state && (
                <span className="browser-design-selection" aria-hidden style={{
                  left: `${selected.rect.x / state.viewport.width * 100}%`,
                  top: `${selected.rect.y / state.viewport.height * 100}%`,
                  width: `${selected.rect.width / state.viewport.width * 100}%`,
                  height: `${selected.rect.height / state.viewport.height * 100}%`,
                }}>
                  <span>{Math.round(selected.rect.width)} × {Math.round(selected.rect.height)}</span>
                </span>
              )}
            </button>
          </div>
          <aside className="browser-design-inspector" aria-label="元素样式">
            <div className="browser-design-inspector-scroll">
              {!selected && (
                <div className="browser-design-empty">
                  <IconPointer size={28} stroke={1.5} />
                  <Text size="sm">点选需要标注的区域</Text>
                  <Text size="xs" c="dimmed">选择后即可调整尺寸、颜色和间距</Text>
                </div>
              )}
              {selected && values && (
                <Stack gap={16}>
                  <div className="browser-design-element">
                    <Group justify="space-between">
                      <Text size="sm" fw={600}>选中区域</Text>
                      <Tooltip label="选取父元素">
                        <ActionIcon variant="subtle" size={28} aria-label="选取父元素" disabled={busy || !selected.has_parent} onClick={() => void action({ action: 'parent' })}>
                          <IconArrowUpLeft size={16} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                    {selected.text && <Text size="xs" c="dimmed" lineClamp={2}>{selected.text}</Text>}
                  </div>
                  {numericGroups.map(group => (
                    <fieldset className="browser-design-fields" key={group.title}>
                      <legend>{group.title}</legend>
                      <div>
                        {group.fields.map(property => (
                          <NumberInput
                            key={`${selected.id}:${property}`} name={property} label={designProperties[property]} size="xs"
                            value={Number.isFinite(Number.parseFloat(values[property])) ? Number.parseFloat(values[property]) : ''}
                            placeholder={values[property] === 'normal' ? '默认' : values[property]}
                            suffix=" px" min={property.startsWith('margin-') ? -1000 : 0} max={4000} decimalScale={1} step={1}
                            onChange={value => { if (typeof value === 'number') change(property, `${value}px`) }}
                          />
                        ))}
                      </div>
                    </fieldset>
                  ))}
                  <fieldset className="browser-design-fields">
                    <legend>颜色</legend>
                    <div>
                      {(['color', 'background-color'] as const).map(property => {
                        const hex = cssColorToHex(values[property])
                        return (
                          <label className="browser-design-color" key={property}>
                            <span>{designProperties[property]}</span>
                            <span className="browser-design-color-value">
                              <input type="color" name={property} value={hex || '#ffffff'} onChange={event => change(property, event.currentTarget.value)} />
                              <span>{hex || values[property]}</span>
                            </span>
                          </label>
                        )
                      })}
                    </div>
                  </fieldset>
                </Stack>
              )}
              {Boolean(count) && (
                <div className="browser-design-changes">
                  <Text size="xs" fw={600}>本次调整</Text>
                  {state?.changes.map(entry => (
                    <div key={entry.element_id}>
                      <code title={entry.selector}>{entry.tag}{entry.text ? ` · ${entry.text.slice(0, 32)}` : ''}</code>
                      {entry.properties.map(edit => (
                        <div className="browser-design-change" key={edit.property}>
                          <span>{designProperties[edit.property]}</span>
                          <span><del>{edit.before}</del><span aria-hidden> → </span>{edit.after}</span>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              )}
            </div>
            <form className="browser-design-submit" onSubmit={event => { event.preventDefault(); void addToChat() }}>
              <Textarea name="note" label="补充要求" placeholder="还希望怎样调整？" value={note} onChange={event => setNote(event.currentTarget.value)} size="xs" autosize minRows={2} maxRows={5} />
              <Button type="submit" fullWidth size="sm" color="violet" disabled={busy || (!count && !(selected && note.trim())) || added} loading={adding} leftSection={added ? <IconCheck size={15} /> : <IconMessagePlus size={15} />}>
                {added ? '已加入对话' : '加入当前对话'}
              </Button>
            </form>
          </aside>
        </div>
      </div>
    </Modal>
  )
}
