import { Alert, Button, Card, Group, Kbd, Stack, Text, TextInput, Title } from '@mantine/core'
import { IconChevronRight, IconInfoCircle, IconKeyboard } from '@tabler/icons-react'
import { useState } from 'react'
import { shortcutActions, shortcutFromEvent, useShortcutStore, type ShortcutAction } from './shortcuts'
import { useSettingsMode } from '../settings/settingsMode'

const ADVANCED_ONLY_SHORTCUT_IDS = new Set<ShortcutAction>([
  'monitor',
  'fileTree',
  'terminal',
  'blackboard',
  'memory',
  'agentGraph',
])

export function ShortcutSettingsPage() {
  const bindings = useShortcutStore(state => state.bindings)
  const { mode, setMode, managedByWorkbench } = useSettingsMode()
  const isBasicMode = managedByWorkbench && mode === 'basic'
  const [editing, setEditing] = useState<ShortcutAction | null>(null)
  const [candidate, setCandidate] = useState('')
  const [error, setError] = useState<string | null>(null)
  const visibleActions = isBasicMode
    ? shortcutActions.filter((action) => !ADVANCED_ONLY_SHORTCUT_IDS.has(action.id))
    : shortcutActions
  const hiddenCount = shortcutActions.length - visibleActions.length
  const update = (id: ShortcutAction, value: string | null) => {
    const message = useShortcutStore.getState().update(id, value)
    setError(message)
    if (!message) setEditing(null)
  }
  return (
    <Stack gap="lg" maw={960} mx="auto">
      <Group justify="space-between">
        <Group gap="sm">
          <IconKeyboard size={24} />
          <div>
            <Title order={3} size="h4">快捷键</Title>
            <Text size="sm" c="dimmed">修改后立即生效，自动保存在本机。Mac 使用 Command 代替 Ctrl。</Text>
          </div>
        </Group>
        <Button variant="light" onClick={() => {
          const message = useShortcutStore.getState().reset()
          setError(message)
          if (!message) setEditing(null)
        }}>恢复默认快捷键</Button>
      </Group>
      <Text size="sm" c="dimmed">点击修改后直接按下组合键，按 Esc 取消。当前会话操作需要先打开会话；编辑器内的复制、粘贴、保存等快捷键保持原有用法。</Text>
      {isBasicMode && hiddenCount > 0 && (
        <div className="st-inline-notice">
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <IconInfoCircle size={14} aria-hidden="true" />
            <span>已隐藏 {hiddenCount} 项面向开发工具的快捷键</span>
          </span>
          <button type="button" className="st-text-link" onClick={() => setMode('advanced')}>
            <span>在高级配置中查看全部</span>
            <IconChevronRight size={14} aria-hidden="true" />
          </button>
        </div>
      )}
      {error && <Alert color="red" role="alert">{error}</Alert>}
      {['通用', '导航', '当前会话'].map(group => {
        const groupActions = visibleActions.filter(action => action.group === group)
        if (groupActions.length === 0) return null
        return (
          <Stack key={group} gap="sm">
            <Text size="sm" fw={600}>{group}</Text>
            {groupActions.map(action => (
            <Card withBorder radius="md" p="md" key={action.id}>
              <Group justify="space-between" wrap="wrap" gap="md">
                <div>
                  <Text size="sm" fw={600}>{action.label}</Text>
                  <Text size="xs" c="dimmed">{action.description}</Text>
                </div>
                {editing === action.id ? (
                  <Group gap="xs" data-shortcut-recording="true">
                    <TextInput
                      autoFocus readOnly value={candidate} placeholder="请按下组合键"
                      aria-label={`录入${action.label}快捷键`}
                      onKeyDown={event => {
                        if (event.key === 'Tab') return
                        event.preventDefault()
                        event.stopPropagation()
                        if (event.key === 'Escape') { setEditing(null); setError(null); return }
                        const key = shortcutFromEvent(event.nativeEvent)
                        if (key) { setCandidate(key); setError(null) }
                        else if (!['Control', 'Meta', 'Alt', 'Shift'].includes(event.key)) {
                          setCandidate('')
                          setError('请使用 Ctrl、Alt 与按键的组合，或 F1–F12；编辑和系统快捷键不可占用。')
                        }
                      }}
                    />
                    <Button size="xs" disabled={!candidate} onClick={() => update(action.id, candidate)}>保存快捷键</Button>
                    <Button size="xs" variant="subtle" onClick={() => { setEditing(null); setError(null) }}>取消修改</Button>
                  </Group>
                ) : (
                  <Group gap="sm">
                    {bindings[action.id] ? <Kbd>{bindings[action.id]}</Kbd> : <Text size="xs" c="dimmed">未设置</Text>}
                    <Button size="xs" variant="light" aria-label={`修改${action.label}快捷键`} onClick={() => { setEditing(action.id); setCandidate(''); setError(null) }}>修改</Button>
                    <Button size="xs" variant="subtle" color="gray" disabled={!bindings[action.id]} aria-label={`停用${action.label}快捷键`} onClick={() => update(action.id, null)}>停用</Button>
                  </Group>
                )}
              </Group>
            </Card>
            ))}
          </Stack>
        )
      })}
    </Stack>
  )
}
