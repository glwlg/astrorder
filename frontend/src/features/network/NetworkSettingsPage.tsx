import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  CopyButton,
  Divider,
  Group,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import {
  IconCheck,
  IconCopy,
  IconDeviceMobile,
  IconKey,
  IconPlus,
  IconRefresh,
  IconTrash,
  IconWorld,
  IconServer,
  IconShieldCheck,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'
import { JevSettingsCard } from '../services/JevSettingsCard'

export function NetworkSettingsPage() {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [publicUrl, setPublicUrl] = useState('https://ao.651971564.xyz')
  const [allowedOrigins, setAllowedOrigins] = useState<string[]>([])
  const [localIp, setLocalIp] = useState('127.0.0.1')
  const [port, setPort] = useState(30001)
  const [token, setToken] = useState('')
  const [newOrigin, setNewOrigin] = useState('')

  const fetchConfig = async () => {
    setLoading(true)
    try {
      const res = await api.getNetworkConfig()
      setPublicUrl(res.public_url || 'https://ao.651971564.xyz')
      setAllowedOrigins(res.allowed_origins || [])
      setLocalIp(res.local_ip || '127.0.0.1')
      setPort(res.port || 30001)
      setToken(res.token || '')
    } catch {
      // fallback
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchConfig()
  }, [])

  const handleAddOrigin = (originToAdd?: string) => {
    const candidate = (originToAdd || newOrigin).trim().replace(/\/+$/, '')
    if (!candidate) return
    if (!allowedOrigins.includes(candidate)) {
      setAllowedOrigins((prev) => [...prev, candidate])
    }
    if (!originToAdd) setNewOrigin('')
  }

  const handleRemoveOrigin = (originToRemove: string) => {
    setAllowedOrigins((prev) => prev.filter((o) => o !== originToRemove))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const cleanedOrigins = Array.from(new Set(allowedOrigins.map((o) => o.trim().replace(/\/+$/, '')).filter(Boolean)))
      // 确保 publicUrl 也在 allowedOrigins 中
      if (publicUrl.trim() && !cleanedOrigins.includes(publicUrl.trim().replace(/\/+$/, ''))) {
        cleanedOrigins.push(publicUrl.trim().replace(/\/+$/, ''))
      }

      await api.updateNetworkConfig({
        public_url: publicUrl.trim(),
        allowed_origins: cleanedOrigins,
      })

      notifications.show({
        color: 'teal',
        message: '网络与移动端访问配置已成功保存并即时生效',
      })
      void fetchConfig()
    } catch (e: any) {
      notifications.show({
        color: 'red',
        message: e.message || '保存网络配置失败',
      })
    } finally {
      setSaving(false)
    }
  }

  const localLanUrl = `http://${localIp}:${port}`

  return (
    <div style={{ maxWidth: 960, margin: '0 auto', padding: '12px 0' }}>
      <Stack gap="lg">
        {/* 1. 顶部标题卡片 */}
        <Paper p="md" withBorder radius="lg" style={{ background: 'var(--astr-surface)' }}>
          <Group justify="space-between" align="center">
            <Group gap="sm">
              <div
                style={{
                  width: 38,
                  height: 38,
                  borderRadius: 10,
                  background: 'rgba(91, 91, 214, 0.1)',
                  color: 'var(--astr-indigo, #5B5BD6)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <IconWorld size={22} />
              </div>
              <div>
                <Title order={3} size="h4">
                  网络与移动端访问配置
                </Title>
                <Text size="xs" c="dimmed" mt={2}>
                  配置移动端 PWA、内网穿透域名及 CORS 跨域访问白名单。所有配置保存在共享数据库中，即时生效。
                </Text>
              </div>
            </Group>

            <Button
              size="xs"
              variant="light"
              leftSection={<IconRefresh size={14} />}
              onClick={fetchConfig}
              loading={loading}
            >
              刷新
            </Button>
          </Group>
        </Paper>

        {/* 2. 移动端 PWA 与外网访问卡片 */}
        <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
          <Group justify="space-between" mb="sm">
            <Group gap="xs">
              <IconDeviceMobile size={18} color="#5B5BD6" />
              <Text fw={600} size="sm">
                移动端 (PWA) 与外网访问地址
              </Text>
            </Group>
            <Badge size="xs" variant="light" color="indigo">
              免重启即时生效
            </Badge>
          </Group>

          <Stack gap="sm">
            <TextInput
              label="公网 / 内网穿透域名 (Public URL)"
              description="手机浏览器或 PWA 应用通过此地址访问星序工作台（例如 Cloudflare Tunnel / FRP 域名）"
              placeholder="https://ao.651971564.xyz"
              value={publicUrl}
              onChange={(e) => setPublicUrl(e.currentTarget.value)}
              disabled={saving}
            />

            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
                padding: '10px 14px',
                borderRadius: 8,
                background: 'var(--astr-surface-muted, #F8FAFC)',
                border: '1px solid var(--astr-border, #E5E7EB)',
                alignItems: 'center',
                justifyContent: 'space-between',
              }}
            >
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontSize: 11, color: '#64748B' }}>移动端一键快捷凭据</span>
                <span style={{ fontSize: 12, fontWeight: 600, fontFamily: 'monospace', color: '#1E293B' }}>
                  {publicUrl}
                </span>
              </div>

              <Group gap="xs">
                <CopyButton value={publicUrl}>
                  {({ copied, copy }) => (
                    <Button
                      size="xs"
                      variant="light"
                      color={copied ? 'teal' : 'indigo'}
                      onClick={copy}
                      leftSection={copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                    >
                      {copied ? '已复制链接' : '复制移动端链接'}
                    </Button>
                  )}
                </CopyButton>

                {token && (
                  <CopyButton value={token}>
                    {({ copied, copy }) => (
                      <Button
                        size="xs"
                        variant="default"
                        onClick={copy}
                        leftSection={copied ? <IconCheck size={14} /> : <IconKey size={14} />}
                      >
                        {copied ? '已复制令牌' : '复制访问令牌 (Token)'}
                      </Button>
                    )}
                  </CopyButton>
                )}
              </Group>
            </div>

            <Group justify="space-between" align="center" style={{ fontSize: 11, color: '#64748B', paddingTop: 2 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <IconServer size={14} />
                <span>本机局域网直连地址: </span>
                <code style={{ fontFamily: 'monospace', color: '#5B5BD6' }}>{localLanUrl}</code>
              </div>
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                onClick={() => handleAddOrigin(localLanUrl)}
              >
                + 添加局域网 IP 至允许来源
              </Button>
            </Group>
          </Stack>
        </Card>

        {/* 2.5 Jev 模型服务配置 */}
        <JevSettingsCard />

        {/* 3. 跨域白名单 (Allowed Origins) 卡片 */}
        <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
          <Group justify="space-between" mb="xs">
            <div>
              <Group gap="xs">
                <IconShieldCheck size={18} color="#059669" />
                <Text fw={600} size="sm">
                  跨域白名单与允许来源 (Allowed Origins)
                </Text>
              </Group>
              <Text size="xs" c="dimmed" mt={2}>
                只有在此列表中的域名或来源，才被允许访问星序 API、建立 WebSocket 及登录会话。
              </Text>
            </div>
            <Badge size="xs" variant="light" color="teal">
              已配置 {allowedOrigins.length} 项
            </Badge>
          </Group>

          <Divider my="sm" />

          <Stack gap="sm">
            {/* 添加新来源栏 */}
            <Group gap="xs">
              <TextInput
                placeholder="例如 https://ao.651971564.xyz 或 http://192.168.1.11:30001"
                value={newOrigin}
                onChange={(e) => setNewOrigin(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    handleAddOrigin()
                  }
                }}
                style={{ flex: 1 }}
                disabled={saving}
              />
              <Button
                color="indigo"
                leftSection={<IconPlus size={14} />}
                onClick={() => handleAddOrigin()}
                disabled={!newOrigin.trim() || saving}
              >
                添加来源
              </Button>
            </Group>

            {/* 快捷推荐来源芯片 */}
            <Group gap={6} align="center">
              <Text size="11px" c="dimmed">
                快捷添加:
              </Text>
              {publicUrl && !allowedOrigins.includes(publicUrl.trim().replace(/\/+$/, '')) && (
                <Button
                  size="compact-xs"
                  variant="light"
                  color="cyan"
                  onClick={() => handleAddOrigin(publicUrl)}
                >
                  + 公网域名 ({publicUrl})
                </Button>
              )}
              {!allowedOrigins.includes(localLanUrl) && (
                <Button
                  size="compact-xs"
                  variant="light"
                  color="blue"
                  onClick={() => handleAddOrigin(localLanUrl)}
                >
                  + 局域网 ({localLanUrl})
                </Button>
              )}
              {!allowedOrigins.includes('http://localhost:30001') && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  onClick={() => handleAddOrigin('http://localhost:30001')}
                >
                  + localhost:30001
                </Button>
              )}
            </Group>

            {/* 来源清单 */}
            <div
              style={{
                borderRadius: 8,
                border: '1px solid var(--astr-border, #E5E7EB)',
                background: 'var(--astr-surface-muted, #F8FAFC)',
                padding: '8px 10px',
                display: 'flex',
                flexDirection: 'column',
                gap: 6,
                maxHeight: 260,
                overflowY: 'auto',
              }}
            >
              {allowedOrigins.length > 0 ? (
                allowedOrigins.map((origin) => {
                  const isLocal = origin.includes('127.0.0.1') || origin.includes('localhost')
                  const isPublic = origin === publicUrl.trim().replace(/\/+$/, '')

                  return (
                    <div
                      key={origin}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        padding: '6px 10px',
                        borderRadius: 6,
                        background: 'var(--astr-surface, #ffffff)',
                        border: '1px solid var(--astr-border, #E5E7EB)',
                        fontSize: 12,
                      }}
                    >
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <code style={{ fontFamily: 'monospace', fontWeight: 600, color: '#1E293B' }}>
                          {origin}
                        </code>
                        {isPublic && (
                          <Badge size="xs" color="indigo" variant="light">
                            当前公网域名
                          </Badge>
                        )}
                        {isLocal && (
                          <Badge size="xs" color="gray" variant="light">
                            本地回环
                          </Badge>
                        )}
                      </div>

                      <Tooltip label="移除该来源" withArrow>
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="red"
                          onClick={() => handleRemoveOrigin(origin)}
                          disabled={saving}
                        >
                          <IconTrash size={13} />
                        </ActionIcon>
                      </Tooltip>
                    </div>
                  )
                })
              ) : (
                <div style={{ textAlign: 'center', padding: 16, fontSize: 12, color: '#94A3B8' }}>
                  暂未配置任何允许来源
                </div>
              )}
            </div>
          </Stack>
        </Card>

        {/* 保存控制区 */}
        <Group justify="flex-end" gap="sm">
          <Button
            size="md"
            color="indigo"
            onClick={handleSave}
            loading={saving}
            leftSection={<IconCheck size={16} />}
            style={{ minWidth: 140, boxShadow: '0 2px 8px rgba(91, 91, 214, 0.25)' }}
          >
            保存配置并立即生效
          </Button>
        </Group>
      </Stack>
    </div>
  )
}