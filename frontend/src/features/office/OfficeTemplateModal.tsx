import { useEffect, useMemo, useState } from 'react'
import { Badge, Button, Group, Modal, Select, Stack, Text, Textarea, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconFileText, IconMessageForward, IconSparkles } from '@tabler/icons-react'
import { api } from '../../api/client'
import { useOfficePersona } from './officePersona'
import {
  assembleTemplatePrompt,
  OFFICE_DRAFT_TEMPLATES,
  type OfficeDraftTemplate,
} from './officeTemplates'

export interface OfficeTemplateModalProps {
  opened: boolean
  initialTemplateId?: string | null
  workspace?: string | null
  sessionId?: string | null
  connectionId?: string | null
  pinnedSourcePaths?: string[]
  onClose: () => void
  /** Send assembled drafting prompt into current/new chat conversation */
  onSendToChat: (prompt: string, title: string) => void | Promise<void>
  /** Open generated Markdown directly inside Rich Document Studio */
  onGeneratedDocument?: (doc: { title: string; markdown: string; savedPath?: string }) => void
}

export function OfficeTemplateModal({
  opened,
  initialTemplateId,
  workspace,
  sessionId,
  connectionId,
  pinnedSourcePaths = [],
  onClose,
  onSendToChat,
  onGeneratedDocument,
}: OfficeTemplateModalProps) {
  const { profile } = useOfficePersona()
  const [activeId, setActiveId] = useState<string>(initialTemplateId || OFFICE_DRAFT_TEMPLATES[0].id)
  const [values, setValues] = useState<Record<string, string>>({})
  const [generating, setGenerating] = useState(false)

  const template = useMemo<OfficeDraftTemplate>(
    () => OFFICE_DRAFT_TEMPLATES.find((t) => t.id === activeId) || OFFICE_DRAFT_TEMPLATES[0],
    [activeId],
  )

  useEffect(() => {
    if (!opened) return
    const nextId = initialTemplateId || OFFICE_DRAFT_TEMPLATES[0].id
    setActiveId(nextId)
    const tpl = OFFICE_DRAFT_TEMPLATES.find((t) => t.id === nextId) || OFFICE_DRAFT_TEMPLATES[0]
    const defaults: Record<string, string> = {}
    for (const f of tpl.fields) {
      if (f.defaultValue) defaults[f.key] = f.defaultValue
      if (f.key === 'audience' && profile.default_audience) {
        defaults[f.key] = profile.default_audience
      }
      if (f.key === 'issuer' && profile.department) {
        defaults[f.key] = profile.department
      }
    }
    setValues(defaults)
  }, [initialTemplateId, opened, profile.default_audience, profile.department])

  const selectTemplate = (tpl: OfficeDraftTemplate) => {
    setActiveId(tpl.id)
    const defaults: Record<string, string> = {}
    for (const f of tpl.fields) {
      if (f.defaultValue) defaults[f.key] = f.defaultValue
      if (f.key === 'audience' && profile.default_audience) defaults[f.key] = profile.default_audience
      if (f.key === 'issuer' && profile.department) defaults[f.key] = profile.department
    }
    setValues(defaults)
  }

  const missingRequired = template.fields.some((f) => f.required && !(values[f.key] || '').trim())

  const handleSendToChat = async () => {
    if (missingRequired) {
      notifications.show({ color: 'yellow', message: '请先填写带 * 的必填项' })
      return
    }
    const { title, prompt } = assembleTemplatePrompt(template, values, profile)
    onClose()
    await onSendToChat(prompt, title)
  }

  const handleGenerateDirect = async () => {
    if (missingRequired) {
      notifications.show({ color: 'yellow', message: '请先填写带 * 的必填项' })
      return
    }
    const { title, prompt } = assembleTemplatePrompt(template, values, profile)
    setGenerating(true)
    try {
      const resp = await api.synthesizeOfficeSources({
        paths: pinnedSourcePaths.length > 0 ? pinnedSourcePaths : undefined,
        action: template.id === 'ppt_deck' ? 'ppt_outline' : template.id === 'contract_review' ? 'review' : 'draft',
        title,
        instruction: prompt,
        session_id: sessionId || undefined,
        connection_id: connectionId || undefined,
        mode: 'llm',
      })
      if (resp.mode === 'prompt_only') {
        notifications.show({
          color: 'yellow',
          message: '尚未配置直连 AI 供应商，已将起草指令发送到对话由助手为您生成',
        })
        onClose()
        await onSendToChat(resp.assembled_prompt, title)
        return
      }

      let savedPath: string | undefined
      if (workspace || sessionId) {
        try {
          const saved = await api.saveOfficeExport({
            format: 'md',
            markdown: resp.markdown,
            title: resp.title || title,
            file_name: `${resp.title || title}`,
            workspace: workspace || undefined,
            session_id: sessionId || undefined,
            connection_id: connectionId || undefined,
          })
          savedPath = saved.path
        } catch {
          // Fall back to inline studio view if workspace save is unavailable
        }
      }
      onClose()
      onGeneratedDocument?.({
        title: resp.title || title,
        markdown: resp.markdown,
        savedPath,
      })
      notifications.show({
        color: 'teal',
        message: savedPath ? '已生成成品文档并保存至工作区，正在写作台打开' : '已生成成品文档，正在写作台打开',
      })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '生成文档失败',
      })
    } finally {
      setGenerating(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={() => !generating && onClose()}
      title={
        <Group gap={8}>
          <IconFileText size={18} />
          <Text fw={650} size="sm">场景模板起草</Text>
          {pinnedSourcePaths.length > 0 && (
            <Badge size="xs" variant="light" color="gray">
              已附带 {pinnedSourcePaths.length} 份参考资料
            </Badge>
          )}
        </Group>
      }
      size="lg"
      radius="md"
      withinPortal
    >
      <div style={{ display: 'grid', gridTemplateColumns: '176px minmax(0, 1fr)', gap: 16 }}>
        {/* Left template rail */}
        <div
          style={{
            borderRight: '1px solid var(--astr-border)',
            paddingRight: 10,
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
            maxHeight: 420,
            overflowY: 'auto',
          }}
        >
          {OFFICE_DRAFT_TEMPLATES.map((tpl) => {
            const active = tpl.id === template.id
            return (
              <button
                key={tpl.id}
                type="button"
                onClick={() => selectTemplate(tpl)}
                style={{
                  textAlign: 'left',
                  padding: '8px 10px',
                  borderRadius: 8,
                  border: 'none',
                  cursor: 'pointer',
                  background: active ? '#18181b' : 'transparent',
                  color: active ? '#ffffff' : 'var(--astr-text)',
                  transition: 'background-color 120ms ease',
                }}
              >
                <div style={{ fontSize: 12.5, fontWeight: active ? 600 : 500 }}>{tpl.shortLabel}</div>
                <div style={{ fontSize: 10.5, opacity: active ? 0.72 : 0.6 }}>{tpl.category}</div>
              </button>
            )
          })}
        </div>

        {/* Right structured form */}
        <Stack gap="sm">
          <div>
            <Text size="sm" fw={650}>{template.name}</Text>
            <Text size="xs" c="dimmed">{template.description}</Text>
          </div>

          {template.fields.map((field) =>
            field.options ? (
              <Select
                key={field.key}
                size="xs"
                label={field.required ? `${field.label} *` : field.label}
                data={field.options}
                value={values[field.key] || field.defaultValue || ''}
                onChange={(val) => {
                  setValues((prev) => ({ ...prev, [field.key]: val || '' }))
                }}
              />
            ) : field.multiline ? (
              <Textarea
                key={field.key}
                size="xs"
                label={field.required ? `${field.label} *` : field.label}
                placeholder={field.placeholder}
                minRows={2}
                autosize
                value={values[field.key] || ''}
                onChange={(e) => {
                  const val = e.currentTarget.value
                  setValues((prev) => ({ ...prev, [field.key]: val }))
                }}
              />
            ) : (
              <TextInput
                key={field.key}
                size="xs"
                label={field.required ? `${field.label} *` : field.label}
                placeholder={field.placeholder}
                value={values[field.key] || ''}
                onChange={(e) => {
                  const val = e.currentTarget.value
                  setValues((prev) => ({ ...prev, [field.key]: val }))
                }}
              />
            ),
          )}

          <Group justify="flex-end" gap="xs" mt="xs">
            <Button size="xs" variant="subtle" color="gray" disabled={generating} onClick={onClose}>
              取消
            </Button>
            {onGeneratedDocument && (
              <Button
                size="xs"
                variant="default"
                loading={generating}
                leftSection={<IconSparkles size={13} />}
                onClick={() => void handleGenerateDirect()}
              >
                直接生成到写作台
              </Button>
            )}
            <Button
              size="xs"
              color="dark"
              disabled={generating}
              leftSection={<IconMessageForward size={13} />}
              onClick={() => void handleSendToChat()}
            >
              在对话中起草
            </Button>
          </Group>
        </Stack>
      </div>
    </Modal>
  )
}
