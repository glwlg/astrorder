import { useEffect, useState } from 'react'
import { Badge, Button, Card, Group, PasswordInput, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconBrain, IconCheck, IconRefresh } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'

type Form = { base_url: string; model: string; reasoning: string; api_key: string }

export function LlmSettingsCard() {
  const [form, setForm] = useState<Form>({ base_url: '', model: '', reasoning: 'low', api_key: '' })
  const [configured, setConfigured] = useState(false)
  const [maskedKey, setMaskedKey] = useState('')
  const [busy, setBusy] = useState<'load' | 'save' | 'test' | null>('load')

  const load = async () => {
    setBusy('load')
    try {
      const result = await api.getLlmConfig()
      setForm({ base_url: result.base_url, model: result.model, reasoning: result.reasoning, api_key: '' })
      setConfigured(result.configured)
      setMaskedKey(result.masked_key)
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '读取 LLM 配置失败' })
    } finally {
      setBusy(null)
    }
  }

  useEffect(() => { void load() }, [])

  const payload = () => ({
    base_url: form.base_url.trim(),
    model: form.model.trim(),
    reasoning: form.reasoning,
    ...(form.api_key.trim() ? { api_key: form.api_key.trim() } : {}),
  })

  const save = async () => {
    setBusy('save')
    try {
      const result = await api.updateLlmConfig(payload())
      setConfigured(result.configured)
      setMaskedKey(result.masked_key)
      setForm((value) => ({ ...value, api_key: '' }))
      notifications.show({ color: 'teal', message: '通用 LLM 配置已保存', icon: <IconCheck size={16} /> })
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '保存 LLM 配置失败' })
    } finally {
      setBusy(null)
    }
  }

  const test = async () => {
    setBusy('test')
    try {
      const result = await api.testLlmConnection(payload())
      notifications.show({ color: 'teal', title: '连接成功', message: `${result.model}: ${result.reply}` })
    } catch (error: any) {
      notifications.show({ color: 'red', title: '连接失败', message: error.message || 'LLM 调用失败' })
    } finally {
      setBusy(null)
    }
  }

  const valid = Boolean(form.base_url.trim() && form.model.trim() && (configured || form.api_key.trim()))

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" mb="md">
        <Group gap="xs">
          <IconBrain size={18} color="#7C3AED" />
          <div>
            <Text fw={600} size="sm">通用 LLM</Text>
            <Text size="xs" c="dimmed">供浏览器文本填写及星序智能功能使用</Text>
          </div>
        </Group>
        <Badge size="xs" variant="light" color={configured ? 'teal' : 'gray'}>
          {configured ? `已配置 ${maskedKey}` : '未配置'}
        </Badge>
      </Group>
      <Stack gap="sm">
        <TextInput label="OpenAI 兼容 API URL" placeholder="https://api.example.com/v1" value={form.base_url} onChange={(event) => setForm({ ...form, base_url: event.currentTarget.value })} />
        <Group grow align="flex-start">
          <PasswordInput label="API Key" placeholder={configured ? '留空则保留当前 Key' : '填写 API Key'} value={form.api_key} onChange={(event) => setForm({ ...form, api_key: event.currentTarget.value })} />
          <TextInput label="模型" placeholder="model-name" value={form.model} onChange={(event) => setForm({ ...form, model: event.currentTarget.value })} />
          <Select label="思考程度" value={form.reasoning} onChange={(value) => setForm({ ...form, reasoning: value || 'low' })} data={[{ value: 'none', label: '关闭' }, { value: 'low', label: '低' }, { value: 'medium', label: '中' }, { value: 'high', label: '高' }]} />
        </Group>
        <Group justify="flex-end">
          <Button variant="light" leftSection={<IconRefresh size={14} />} disabled={!valid} loading={busy === 'test'} onClick={test}>测试连接</Button>
          <Button color="violet" disabled={!valid} loading={busy === 'save'} onClick={save}>保存配置</Button>
        </Group>
      </Stack>
    </Card>
  )
}
