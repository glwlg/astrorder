import { useEffect, useState } from 'react'
import { Button, Group, SimpleGrid, Stack, Switch, Text, Textarea, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCheck, IconUserCog } from '@tabler/icons-react'
import type { OfficePersonaProfile } from '../../api/client'
import {
  DEFAULT_OFFICE_PERSONA,
  useOfficePersona,
  WRITING_STYLE_OPTIONS,
} from './officePersona'

export interface OfficePersonaCardProps {
  compact?: boolean
  onSaved?: (profile: OfficePersonaProfile) => void
}

export function OfficePersonaCard({ compact = false, onSaved }: OfficePersonaCardProps) {
  const { profile, save } = useOfficePersona()
  const [draft, setDraft] = useState<OfficePersonaProfile>(profile)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setDraft(profile)
  }, [profile])

  const handleSave = async () => {
    setSaving(true)
    try {
      const saved = await save(draft)
      notifications.show({ color: 'teal', message: '已保存个人办公画像与写作偏好' })
      onSaved?.(saved)
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '保存办公画像失败',
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      style={{
        border: compact ? 'none' : '1px solid var(--st-border, var(--astr-border))',
        borderRadius: compact ? 0 : 12,
        padding: compact ? 0 : '16px 18px',
        background: compact ? 'transparent' : 'var(--st-surface, var(--astr-surface))',
      }}
    >
      <Stack gap="sm">
        {!compact && (
          <Group justify="space-between" align="flex-start">
            <Group gap={8}>
              <IconUserCog size={18} />
              <div>
                <Text size="sm" fw={650}>个人办公画像与写作偏好</Text>
                <Text size="xs" c="dimmed">
                  配置一次，在起草文档、局部精修、工作周报与会议纪要中自动贴合你的岗位口径与表达风格
                </Text>
              </div>
            </Group>
          </Group>
        )}

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
          <TextInput
            size="xs"
            label="你的称呼 / 姓名"
            placeholder="例如：林经理 / 张明"
            value={draft.display_name}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, display_name: val }))
            }}
          />
          <TextInput
            size="xs"
            label="岗位与职责"
            placeholder="例如：产品运营负责人 / 财务分析"
            value={draft.role}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, role: val }))
            }}
          />
          <TextInput
            size="xs"
            label="所属部门 / 团队"
            placeholder="例如：数字化运营部"
            value={draft.department}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, department: val }))
            }}
          />
          <TextInput
            size="xs"
            label="常见汇报 / 沟通对象"
            placeholder="例如：公司高管、外部政企客户"
            value={draft.default_audience}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, default_audience: val }))
            }}
          />
        </SimpleGrid>

        <div>
          <Text size="xs" fw={550} mb={6}>默认行文风格</Text>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 8 }}>
            {WRITING_STYLE_OPTIONS.map((opt) => {
              const active = draft.writing_style === opt.value
              return (
                <button
                  key={opt.value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setDraft((prev) => ({ ...prev, writing_style: opt.value }))}
                  style={{
                    textAlign: 'left',
                    padding: '9px 11px',
                    borderRadius: 9,
                    cursor: 'pointer',
                    background: active ? 'var(--astr-surface, #ffffff)' : 'var(--astr-surface-muted, #f8f8fa)',
                    border: active ? '1.5px solid #18181b' : '1px solid var(--astr-border, rgba(24,24,27,0.08))',
                  }}
                >
                  <div style={{ fontSize: 12.5, fontWeight: 600 }}>{opt.label}</div>
                  <div style={{ fontSize: 11, color: 'var(--astr-muted)', marginTop: 2 }}>{opt.summary}</div>
                </button>
              )
            })}
          </div>
        </div>

        <Textarea
          size="xs"
          label="个性化写作要求"
          placeholder="例如：优先使用结构化列表和表格；数据须说明同比/环比；少用晦涩英文缩写"
          minRows={2}
          autosize
          value={draft.tone_rules}
          onChange={(e) => {
            const val = e.currentTarget.value
            setDraft((prev) => ({ ...prev, tone_rules: val }))
          }}
        />

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
          <TextInput
            size="xs"
            label="常用业务术语 / 专有名词"
            placeholder="用顿号或逗号分隔，如：星序、OKR、GMV"
            value={draft.glossary}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, glossary: val }))
            }}
          />
          <TextInput
            size="xs"
            label="禁用词 / 自动脱敏项"
            placeholder="对外文本中自动隐去的内部代号或敏感词"
            value={draft.avoid_words}
            onChange={(e) => {
              const val = e.currentTarget.value
              setDraft((prev) => ({ ...prev, avoid_words: val }))
            }}
          />
        </SimpleGrid>

        <Group justify="space-between" align="center" mt={4}>
          <Switch
            size="xs"
            label="在办公模板起草与资料包协同中自动套用此画像"
            checked={draft.auto_inject_chat}
            onChange={(e) => {
              const checked = e.currentTarget.checked
              setDraft((prev) => ({ ...prev, auto_inject_chat: checked }))
            }}
          />
          <Group gap="xs">
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              onClick={() => setDraft(DEFAULT_OFFICE_PERSONA)}
            >
              重置
            </Button>
            <Button
              size="xs"
              color="dark"
              loading={saving}
              leftSection={<IconCheck size={13} />}
              onClick={() => void handleSave()}
            >
              保存画像
            </Button>
          </Group>
        </Group>
      </Stack>
    </div>
  )
}
