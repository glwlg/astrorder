import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Divider, Group, Modal, Paper, Stack, Text, Textarea, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconPlus, IconTrash } from '@tabler/icons-react'
import { api, type DigestTemplate } from '../../api/client'

export function DigestTemplateModal({
  opened,
  onClose,
  templates,
  onTemplatesChange,
}: {
  opened: boolean
  onClose: () => void
  templates: DigestTemplate[]
  onTemplatesChange: (next: DigestTemplate[]) => void
}) {
  const [editingId, setEditingId] = useState<string>('')
  const [name, setName] = useState('')
  const [audience, setAudience] = useState('')
  const [description, setDescription] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!opened) {
      setEditingId('')
      setName('')
      setAudience('')
      setDescription('')
      setSystemPrompt('')
    }
  }, [opened])

  const startEdit = (tpl: DigestTemplate) => {
    setEditingId(tpl.built_in ? '' : tpl.id)
    setName(tpl.built_in ? `${tpl.name}（副本）` : tpl.name)
    setAudience(tpl.audience)
    setDescription(tpl.description)
    setSystemPrompt(tpl.system_prompt)
  }

  const handleSave = async () => {
    if (!name.trim() || !systemPrompt.trim()) {
      notifications.show({ color: 'yellow', message: '请填写模板名称与写作提示词' })
      return
    }
    setSaving(true)
    try {
      await api.saveDigestTemplate({
        id: editingId || undefined,
        name: name.trim(),
        audience: audience.trim(),
        description: description.trim(),
        system_prompt: systemPrompt.trim(),
      })
      const refreshed = await api.listDigestTemplates()
      onTemplatesChange(refreshed.items || [])
      setEditingId('')
      setName('')
      setAudience('')
      setDescription('')
      setSystemPrompt('')
      notifications.show({ color: 'teal', message: '已保存自定义复盘模板' })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '保存模板失败',
      })
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await api.deleteDigestTemplate(id)
      const refreshed = await api.listDigestTemplates()
      onTemplatesChange(refreshed.items || [])
      if (editingId === id) {
        setEditingId('')
        setName('')
        setAudience('')
        setDescription('')
        setSystemPrompt('')
      }
      notifications.show({ color: 'teal', message: '已删除自定义模板' })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '删除模板失败',
      })
    }
  }

  return (
    <Modal opened={opened} onClose={onClose} title="管理工作复盘 / 周报模板" size="lg" withinPortal>
      <Stack gap="md">
        <Stack gap={6}>
          <Text size="xs" fw={600} c="dimmed">现有模板（内置模板可点击“基于此新建”派生自定义版本）</Text>
          {templates.map((tpl) => (
            <Paper key={tpl.id} withBorder p="xs" radius="md">
              <Group justify="space-between" wrap="nowrap" gap="xs">
                <div style={{ minWidth: 0 }}>
                  <Group gap={6}>
                    <Text size="sm" fw={600}>{tpl.name}</Text>
                    <Badge size="xs" variant="light" color={tpl.built_in ? 'gray' : 'indigo'}>
                      {tpl.built_in ? '内置' : '自定义'}
                    </Badge>
                    {tpl.audience && <Badge size="xs" variant="outline">{tpl.audience}</Badge>}
                  </Group>
                  {tpl.description && <Text size="xs" c="dimmed" mt={2}>{tpl.description}</Text>}
                </div>
                <Group gap={4} style={{ flexShrink: 0 }}>
                  <Button size="compact-xs" variant="subtle" onClick={() => startEdit(tpl)}>
                    {tpl.built_in ? '基于此新建' : '编辑'}
                  </Button>
                  {!tpl.built_in && (
                    <ActionIcon
                      size="sm"
                      variant="subtle"
                      color="red"
                      aria-label={`删除模板 ${tpl.name}`}
                      onClick={() => void handleDelete(tpl.id)}
                    >
                      <IconTrash size={14} />
                    </ActionIcon>
                  )}
                </Group>
              </Group>
            </Paper>
          ))}
        </Stack>

        <Divider label={editingId ? '编辑自定义模板' : '新建自定义模板'} labelPosition="left" />

        <Stack gap="xs">
          <Group grow>
            <TextInput
              size="xs"
              label="模板名称"
              placeholder="如：部门 OKR 月报"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
            />
            <TextInput
              size="xs"
              label="面向受众（可选）"
              placeholder="如：研发总监 / 产品经理"
              value={audience}
              onChange={(e) => setAudience(e.currentTarget.value)}
            />
          </Group>
          <TextInput
            size="xs"
            label="模板简介（可选）"
            placeholder="说明该模板的适用场景与风格"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <Textarea
            size="xs"
            label="写作指令与格式要求"
            placeholder="告诉模型需要按什么章节组织、是否需要量化表或脱敏规则…"
            minRows={5}
            autosize
            value={systemPrompt}
            onChange={(e) => setSystemPrompt(e.currentTarget.value)}
          />
          <Group justify="flex-end" gap="xs">
            {editingId && (
              <Button
                size="xs"
                variant="subtle"
                onClick={() => {
                  setEditingId('')
                  setName('')
                  setAudience('')
                  setDescription('')
                  setSystemPrompt('')
                }}
              >
                取消编辑
              </Button>
            )}
            <Button
              size="xs"
              leftSection={<IconPlus size={14} />}
              loading={saving}
              onClick={() => void handleSave()}
            >
              {editingId ? '更新模板' : '保存为自定义模板'}
            </Button>
          </Group>
        </Stack>
      </Stack>
    </Modal>
  )
}
