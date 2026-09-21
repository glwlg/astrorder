import { useState, useEffect } from 'react'
import {
  Card,
  Group,
  Stack,
  Text,
  PasswordInput,
  Button,
  Badge,
  Divider,
  Alert,
  Code,
} from '@mantine/core'
import {
  IconCpu,
  IconCheck,
  IconRefresh,
  IconTrash,
  IconSparkles,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'

export function JevSettingsCard() {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [configured, setConfigured] = useState(false)
  const [maskedKey, setMaskedKey] = useState('')
  const [inputKey, setInputKey] = useState('')
  const [testResult, setTestResult] = useState<any>(null)

  const fetchConfig = async () => {
    setLoading(true)
    try {
      const res = await api.getJevConfig()
      setConfigured(res.configured)
      setMaskedKey(res.masked_key)
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchConfig()
  }, [])

  const handleSave = async () => {
    if (!inputKey.trim()) return
    setSaving(true)
    try {
      const res = await api.updateJevConfig({ api_key: inputKey.trim() })
      setConfigured(res.configured)
      setMaskedKey(res.masked_key)
      setInputKey('')
      notifications.show({
        color: 'teal',
        title: '保存成功',
        message: 'TypeSafe Jev API Key 已安全保存并就绪',
        icon: <IconCheck size={16} />,
      })
    } catch (e: any) {
      notifications.show({
        color: 'red',
        title: '保存失败',
        message: e.message || '更新 Jev 配置失败',
      })
    } finally {
      setSaving(false)
    }
  }

  const handleClear = async () => {
    setSaving(true)
    try {
      const res = await api.updateJevConfig({ api_key: null })
      setConfigured(res.configured)
      setMaskedKey('')
      setInputKey('')
      setTestResult(null)
      notifications.show({
        color: 'gray',
        title: '已清除',
        message: 'Jev API Key 已从本地持久化配置中移除',
      })
    } catch (e: any) {
      notifications.show({
        color: 'red',
        title: '清除失败',
        message: e.message,
      })
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const payloadKey = inputKey.trim() || undefined
      const res = await api.testJevConnection({ api_key: payloadKey })
      setTestResult(res.details)
      notifications.show({
        color: 'teal',
        title: 'Jev 模型连通性测试通过',
        message: `决策返回组件: ${res.details?.component} (置信度 ${(res.details?.confidence * 100).toFixed(0)}%)`,
        icon: <IconCheck size={16} />,
      })
    } catch (e: any) {
      notifications.show({
        color: 'red',
        title: '测试失败',
        message: e.message || 'Jev 模型调用失败',
      })
    } finally {
      setTesting(false)
    }
  }

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" mb="xs">
        <div>
          <Group gap="xs">
            <div
              style={{
                width: 26,
                height: 26,
                borderRadius: 6,
                background: 'rgba(16, 185, 129, 0.12)',
                color: '#10B981',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <IconCpu size={16} />
            </div>
            <Text fw={600} size="sm">
              TypeSafe Jev 模型服务 (System One)
            </Text>
          </Group>
          <Text size="xs" c="dimmed" mt={2}>
            用于任务结构化决策、黑板智能路由与低 Token 开销的 Generative UI 卡片自适应渲染，免除主模型手写冗长 JSON 负担。
          </Text>
        </div>
        <Badge size="xs" variant="light" color={configured ? 'teal' : 'gray'}>
          {configured ? '已配置就绪' : '未配置'}
        </Badge>
      </Group>

      <Divider my="sm" />

      <Stack gap="sm">
        {configured && (
          <Group justify="space-between" p="xs" style={{ background: 'var(--astr-surface-muted, #F8FAFC)', borderRadius: 6, border: '1px solid var(--astr-border, #E2E8F0)' }}>
            <div>
              <Text size="11px" c="dimmed">当前生效的 Jev API Key</Text>
              <Code fw={600} style={{ fontSize: 12 }}>{maskedKey}</Code>
            </div>
            <Group gap="xs">
              <Button
                size="xs"
                variant="light"
                color="teal"
                onClick={handleTest}
                loading={testing}
                leftSection={<IconRefresh size={14} />}
              >
                测试连通性
              </Button>
              <Button
                size="xs"
                variant="subtle"
                color="red"
                onClick={handleClear}
                loading={saving}
                leftSection={<IconTrash size={14} />}
              >
                清除配置
              </Button>
            </Group>
          </Group>
        )}

        <Group align="flex-end" gap="xs">
          <PasswordInput
            label={configured ? "更新 Jev API Key" : "配置 Jev API Key"}
            placeholder="apikey_..."
            value={inputKey}
            onChange={(e) => setInputKey(e.currentTarget.value)}
            disabled={saving || loading}
            style={{ flex: 1 }}
          />
          <Button
            color="teal"
            onClick={handleSave}
            disabled={!inputKey.trim() || saving}
            loading={saving}
          >
            保存 Key
          </Button>
          {!configured && inputKey.trim() && (
            <Button
              variant="light"
              color="teal"
              onClick={handleTest}
              loading={testing}
            >
              直接测试
            </Button>
          )}
        </Group>

        {testResult && (
          <Alert color="teal" variant="light" title="Jev 模型实时判定结果" icon={<IconSparkles size={16} />}>
            <Text size="xs">
              推荐组件: <b>{testResult.component}</b> | 置信度: <b>{(testResult.confidence * 100).toFixed(0)}%</b> | Token 消耗: <b>{testResult.usage?.input_tokens || 0} 输入 / {testResult.usage?.output_tokens || 0} 输出</b>
            </Text>
          </Alert>
        )}
      </Stack>
    </Card>
  )
}
