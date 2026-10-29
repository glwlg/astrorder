import { useEffect, useState } from 'react'
import { Button, Card, Group, SegmentedControl, Stack, Text } from '@mantine/core'
import { IconWorld } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api, type BrowserEngine } from '../../api/client'

export function BrowserEngineSettingsCard() {
  const [engine, setEngine] = useState<BrowserEngine>('browser_use')
  const [saved, setSaved] = useState<BrowserEngine | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let mounted = true
    void api.getBrowserEngine().then(value => {
      if (mounted) { setEngine(value.engine); setSaved(value.engine) }
    }).catch(error => notifications.show({ color: 'red', message: error instanceof Error ? error.message : '读取浏览器引擎失败' }))
    return () => { mounted = false }
  }, [])

  const save = async () => {
    setBusy(true)
    try {
      const value = await api.updateBrowserEngine(engine)
      setSaved(value.engine)
      notifications.show({ color: 'teal', message: '浏览器引擎已保存' })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '保存浏览器引擎失败' })
    } finally { setBusy(false) }
  }

  return <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
    <Group gap="xs" mb="md"><IconWorld size={18} /><Text fw={600} size="sm">浏览器自动任务</Text></Group>
    <Stack gap="sm">
      <SegmentedControl fullWidth aria-label="浏览器引擎" value={engine} disabled={saved === null || busy}
        onChange={value => setEngine(value as BrowserEngine)} data={[
          { value: 'browser_use', label: 'Browser Use' },
          { value: 'jev_ultrafast', label: 'Jev Ultrafast' },
        ]} />
      <Text size="xs" c="dimmed">{engine === 'browser_use' ? '使用普通模型服务执行浏览器任务，模型需要支持结构化输出。' : '使用 Jev 模型服务执行浏览器任务；生成文本时使用普通模型服务。'}</Text>
      <Group justify="flex-end"><Button onClick={() => void save()} loading={busy} disabled={saved === null || saved === engine}>保存配置</Button></Group>
    </Stack>
  </Card>
}
