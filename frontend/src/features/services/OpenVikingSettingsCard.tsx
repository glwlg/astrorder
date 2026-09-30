import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Card, Code, Group, PasswordInput, Stack, Table, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconBrain, IconCheck, IconRefresh } from '@tabler/icons-react'
import { api } from '../../api/client'

type Target = { id: string; kind: 'local' | 'wsl' | 'ssh'; name: string; state?: string }
type TargetStatus = {
  target_id: string
  ovcli_status: string
  hooks_enabled: boolean
  plugin_installed?: boolean
  target_url: string
  hermes_status?: string
  hermes_endpoint?: string
  raw_output?: string
}

export function OpenVikingSettingsCard() {
  const [url, setUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [maskedKey, setMaskedKey] = useState('')
  const [healthy, setHealthy] = useState<boolean | null>(null)
  const [serverVersion, setServerVersion] = useState<string | null>(null)
  const [targets, setTargets] = useState<Target[]>([])
  const [statuses, setStatuses] = useState<Record<string, TargetStatus>>({})
  const [loading, setLoading] = useState(false)
  const [applying, setApplying] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const [cfg, health, targetList] = await Promise.all([
        api.getMemoryConfig(),
        api.getMemoryHealth().catch(() => ({ healthy: false, status: 'unreachable', version: undefined })),
        api.getMemoryTargets(),
      ])
      setUrl(cfg.url)
      setMaskedKey(cfg.masked_key)
      setHealthy(health.healthy)
      setServerVersion(health.version || null)
      setTargets(targetList.items)

      // 异步读取各 target 的 status
      const stMap: Record<string, TargetStatus> = {}
      for (const t of targetList.items) {
        try {
          const st = await api.getMemoryTargetStatus(t.id)
          stMap[t.id] = st
        } catch {
          // ignore
        }
      }
      setStatuses(stMap)
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '加载 OpenViking 设置失败' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const saveConfig = async () => {
    setLoading(true)
    try {
      const res = await api.updateMemoryConfig({
        url: url.trim(),
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
      })
      setMaskedKey(res.masked_key)
      setApiKey('')
      notifications.show({ color: 'teal', message: 'OpenViking 服务配置已保存' })
      void load()
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '保存失败' })
      setLoading(false)
    }
  }

  const applyToAll = async () => {
    setApplying(true)
    try {
      const tids = targets.map((t) => t.id)
      const res = await api.applyMemoryConfig(tids)
      const allOk = res.items.every((i) => i.success)
      if (allOk) {
        notifications.show({ color: 'teal', message: '已全量下发 OpenViking 配置并激活各环境 Hooks！' })
      } else {
        notifications.show({ color: 'yellow', message: '部分环境配置下发异常，请查看详情' })
      }
      void load()
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '配置下发失败' })
    } finally {
      setApplying(false)
    }
  }

  return (
    <Card className="openviking-settings-card" withBorder radius="md" p="lg">
      <Group justify="space-between" mb="xs">
        <Group gap="xs">
          <IconBrain size={20} color="var(--astr-indigo, #5b6cff)" />
          <Text fw={600} size="md">OpenViking 记忆与知识库网关</Text>
          {healthy === true && <Text size="xs" c="dimmed">在线{serverVersion ? ` · v${serverVersion}` : ''}</Text>}
          {healthy === false && <Badge color="red" variant="light">离线 / 不可达</Badge>}
        </Group>
        <ActionIcon variant="subtle" color="gray" onClick={load} loading={loading} title="刷新状态">
          <IconRefresh size={16} />
        </ActionIcon>
      </Group>

      <Text size="xs" c="dimmed" mb="md">
        Codex 通过官方 Hooks 与 MCP 插件直连，Hermes 通过内置 Tools 直连。星序负责跨环境（本机、WSL、Debian）一键配发 <Code>ovcli.conf</Code> 并激活 Hooks。
      </Text>

      <Stack gap="xs" mb="md">
        <Group grow align="flex-end">
          <TextInput
            label="OpenViking 服务端地址"
            placeholder="http://192.168.1.100:1933"
            value={url}
            onChange={(e) => setUrl(e.currentTarget.value)}
          />
          <PasswordInput
            label={`API Key (当前: ${maskedKey || '未配置'})`}
            placeholder={maskedKey ? '留空保持原有 Key' : '输入 OpenViking 认证凭据'}
            value={apiKey}
            onChange={(e) => setApiKey(e.currentTarget.value)}
          />
          <Button variant="light" color="indigo" onClick={saveConfig} loading={loading}>
            保存服务端配置
          </Button>
        </Group>
      </Stack>

      <Group justify="space-between" mb="xs">
        <Text size="sm" fw={600}>各 Agent 运行时配置与 Hooks 状态</Text>
        <Button size="xs" variant="filled" color="indigo" onClick={applyToAll} loading={applying}>
          一键全量下发并激活 Hooks
        </Button>
      </Group>

      <div className="settings-table-scroll"><Table highlightOnHover>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>目标环境</Table.Th>
            <Table.Th>类型</Table.Th>
            <Table.Th>ovcli.conf (全局/Codex)</Table.Th>
            <Table.Th>Codex 记忆插件</Table.Th>
            <Table.Th>Codex Hooks</Table.Th>
            <Table.Th>Hermes 状态</Table.Th>
            <Table.Th>当前配置端点</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {targets.map((t) => {
            const st = statuses[t.id]
            return (
              <Table.Tr key={t.id}>
                <Table.Td><Text size="sm" fw={500}>{t.name}</Text></Table.Td>
                <Table.Td><Badge size="xs" variant="outline">{t.kind.toUpperCase()}</Badge></Table.Td>
                <Table.Td>
                  {st?.ovcli_status === 'configured' ? (
                    <Badge color="teal" size="xs" variant="light">已配置</Badge>
                  ) : st?.ovcli_status === 'missing' ? (
                    <Badge color="yellow" size="xs" variant="light">未配置</Badge>
                  ) : (
                    <Badge color="gray" size="xs" variant="light">检测中...</Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  {st?.plugin_installed ? (
                    <IconCheck size={16} aria-label="已安装" className="settings-status-check" />
                  ) : st ? (
                    <Badge color="gray" size="xs" variant="light">未安装</Badge>
                  ) : (
                    <Badge color="gray" size="xs" variant="light">检测中...</Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  {st?.hooks_enabled ? (
                    <IconCheck size={16} aria-label="已启用" className="settings-status-check" />
                  ) : st ? (
                    <Badge color="orange" size="xs" variant="light">未启用</Badge>
                  ) : (
                    <Badge color="gray" size="xs" variant="light">检测中...</Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  {st?.hermes_status === 'configured' ? (
                    <Badge color="teal" size="xs" variant="light">已接入 OV</Badge>
                  ) : st?.hermes_status === 'disabled' ? (
                    <Badge color="yellow" size="xs" variant="light">未配置</Badge>
                  ) : st ? (
                    <Badge color="gray" size="xs" variant="light">未安装</Badge>
                  ) : (
                    <Badge color="gray" size="xs" variant="light">检测中...</Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  <Text size="xs" c="dimmed">{st?.target_url || st?.hermes_endpoint || '-'}</Text>
                </Table.Td>
              </Table.Tr>
            )
          })}
        </Table.Tbody>
      </Table></div>
    </Card>
  )
}
