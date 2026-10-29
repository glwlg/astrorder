import { useCallback, useEffect, useState } from 'react'
import { Alert, Badge, Button, Card, Code, Group, PasswordInput, SegmentedControl, Select, Stack, Text } from '@mantine/core'
import { IconCpu, IconRefresh, IconSparkles, IconTrash } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api, type JevConfig, type JevConfigPatch, type JevTestDetails } from '../../api/client'

const errorMessage = (error: unknown) => error instanceof Error ? error.message : '请求失败，请重试'

export function JevSettingsCard() {
  const [config, setConfig] = useState<JevConfig | null>(null)
  const [mode, setMode] = useState<JevConfig['mode']>('official')
  const [model, setModel] = useState('')
  const [inputKey, setInputKey] = useState('')
  const [models, setModels] = useState<Array<{ id: string; label: string }>>([])
  const [modelError, setModelError] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingModels, setLoadingModels] = useState(false)
  const [busy, setBusy] = useState<'save' | 'test' | 'clear' | null>(null)
  const [testResult, setTestResult] = useState<JevTestDetails | null>(null)

  useEffect(() => {
    let mounted = true
    void api.getJevConfig().then(value => {
      if (!mounted) return
      setConfig(value)
      setMode(value.mode)
      setModel(value.model)
    }).catch(error => notifications.show({ color: 'red', message: errorMessage(error) }))
      .finally(() => { if (mounted) setLoading(false) })
    return () => { mounted = false }
  }, [])

  const loadModels = useCallback(async () => {
    setLoadingModels(true)
    setModelError('')
    setModels([])
    try {
      const value = await api.getJevModels()
      setModels(value.items)
      setConfig(current => current ? { ...current, gateway_type: value.gateway_type, gateway_url: value.gateway_url, gateway_configured: true } : current)
      if (!value.items.length) setModelError('当前网关没有可用的 System One 模型，请先在网关中启用 Jev 或其他决策模型。')
    } catch (error) {
      setModelError(errorMessage(error))
    } finally { setLoadingModels(false) }
  }, [])

  useEffect(() => { if (!loading && mode === 'gateway') void loadModels() }, [loading, mode, loadModels])

  const payload = (): JevConfigPatch => mode === 'gateway'
    ? { mode, model }
    : { mode, ...(inputKey.trim() ? { api_key: inputKey.trim() } : {}) }

  const save = async () => {
    setBusy('save')
    try {
      setConfig(await api.updateJevConfig(payload()))
      setInputKey('')
      setTestResult(null)
      notifications.show({ color: 'teal', message: 'Jev 模型配置已保存' })
    } catch (error) { notifications.show({ color: 'red', message: errorMessage(error) }) }
    finally { setBusy(null) }
  }
  const clearKey = async () => {
    setBusy('clear')
    try {
      setConfig(await api.updateJevConfig({ mode: 'official', api_key: null }))
      setInputKey('')
      setTestResult(null)
      notifications.show({ color: 'gray', message: '已清除保存的 Jev 官方 Key' })
    } catch (error) { notifications.show({ color: 'red', message: errorMessage(error) }) }
    finally { setBusy(null) }
  }
  const test = async () => {
    setBusy('test')
    setTestResult(null)
    try {
      const result = await api.testJevConnection(payload())
      setTestResult(result.details)
      notifications.show({ color: 'teal', message: 'Jev 连通性测试通过' })
    } catch (error) { notifications.show({ color: 'red', message: errorMessage(error) }) }
    finally { setBusy(null) }
  }
  const valid = mode === 'gateway'
    ? Boolean(config?.gateway_configured && models.some(item => item.id === model) && !loadingModels && !modelError)
    : Boolean(inputKey.trim() || config?.masked_key)
  const disabled = loading || Boolean(busy) || !config

  return <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
    <Group justify="space-between" mb="md">
      <Group gap="xs"><IconCpu size={18} /><div>
        <Text fw={600} size="sm">Jev 模型服务（System One）</Text>
        <Text size="xs" c="dimmed">配置决策模型的连接方式。</Text>
      </div></Group>
      <Badge size="xs" variant="light" color={config?.configured ? 'teal' : 'gray'}>{config?.configured ? '已配置' : '未配置'}</Badge>
    </Group>
    <Stack gap="sm">
      <SegmentedControl fullWidth aria-label="Jev 连接方式" value={mode} disabled={disabled} onChange={value => { setMode(value as JevConfig['mode']); setTestResult(null) }} data={[
        { value: 'gateway', label: '通过 LLM 网关' }, { value: 'official', label: 'Jev 官方' },
      ]} />
      {mode === 'gateway' ? <>
        <Text size="xs" c="dimmed">使用当前 LLM 网关的地址和密钥。</Text>
        <Text size="xs">{config?.gateway_type === 'magpie' ? 'Magpie' : 'OpenCodeX'} · {config?.gateway_url}</Text>
        <Group align="flex-end">
          <Select style={{ flex: 1 }} label="System One 模型" placeholder={loadingModels ? '正在读取模型…' : '选择模型'} searchable value={model} onChange={value => { setModel(value || ''); setTestResult(null) }} disabled={disabled || loadingModels} data={models.map(item => ({ value: item.id, label: item.label === item.id ? item.id : `${item.label} · ${item.id}` }))} nothingFoundMessage="没有匹配的模型" />
          <Button variant="subtle" leftSection={<IconRefresh size={14} />} onClick={() => void loadModels()} loading={loadingModels} disabled={disabled}>刷新模型</Button>
        </Group>
        {modelError && <Alert color="red">{modelError}</Alert>}
        {!modelError && !loadingModels && model && !models.some(item => item.id === model) && <Text size="xs" c="orange">当前网关未提供已选模型 {model}，请重新选择。</Text>}
      </> : <>
        {config?.masked_key && <Group justify="space-between">
          <Text size="xs">已保存的官方 Key：<Code>{config.masked_key}</Code></Text>
          <Button size="xs" variant="subtle" color="red" leftSection={<IconTrash size={14} />} onClick={() => void clearKey()} loading={busy === 'clear'} disabled={disabled}>清除官方 Key</Button>
        </Group>}
        <PasswordInput label="Jev 官方 API Key" placeholder={config?.masked_key ? '留空保留已保存的 Key' : '填写 Jev API Key'} value={inputKey} onChange={event => { setInputKey(event.currentTarget.value); setTestResult(null) }} disabled={disabled} />
      </>}
      <Group justify="flex-end">
        <Button variant="light" onClick={() => void test()} loading={busy === 'test'} disabled={disabled || !valid}>测试连通性</Button>
        <Button onClick={() => void save()} loading={busy === 'save'} disabled={disabled || !valid}>保存配置</Button>
      </Group>
      {testResult && <Alert color="teal" title="Jev 模型判定结果" icon={<IconSparkles size={16} />}>
        <Text size="xs">推荐组件：<b>{testResult.component}</b>{typeof testResult.confidence === 'number' && <> · 置信度：<b>{(testResult.confidence * 100).toFixed(0)}%</b></>}{testResult.usage && <> · Token：{testResult.usage.input_tokens ?? 0} 输入 / {testResult.usage.output_tokens ?? 0} 输出</>}</Text>
      </Alert>}
    </Stack>
  </Card>
}
