import { useState } from 'react'
import { Button, Group, Modal, Stack, Text, TextInput, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCheck, IconChevronDown, IconCpu } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSessionModel } from '../../hooks/useSessionModel'
import './SessionModelControl.css'

type Choice = { provider: string; model: string; label: string }
export function SessionModelControl({ session }: { session: Session }) {
  const model = useSessionModel(session)
  const [opened, setOpened] = useState(false)
  const [search, setSearch] = useState('')
  const [choice, setChoice] = useState<Choice | null>(null)
  const [changing, setChanging] = useState(false)
  const options = useQuery({
    queryKey: ['astrorder', 'model-options', session.agent_id, session.id],
    queryFn: () => api.getSessionModels(session.id, session.agent_id),
    enabled: opened,
    staleTime: 30000,
    retry: false,
  })
  const apply = async () => {
    if (!choice || changing) return
    setChanging(true)
    try {
      const result = await model.change(choice.provider, choice.model)
      setOpened(false)
      notifications.show({ message: result.deferred ? '原生运行时已确认，下轮使用新模型' : '原生模型切换已确认', color: 'teal' })
    } catch (error) {
      notifications.show({ message: error instanceof Error ? error.message : '模型切换未确认', color: 'red' })
    } finally { setChanging(false) }
  }
  return <>
    <Group className="session-model-control" gap="xs" wrap="nowrap">

      <Button size="xs" variant="light" aria-label="选择会话模型" title={model.label} leftSection={<IconCpu size={15} />} rightSection={<IconChevronDown size={14} />} onClick={() => { setChoice(null); setSearch(''); setOpened(true) }}><span>{model.label}</span></Button>
    </Group>
    <Modal opened={opened} onClose={() => setOpened(false)} title="切换会话模型" size="lg" centered>
      <Stack gap="sm">
        <Text size="sm">当前模型：{model.label}</Text>
        <TextInput aria-label="搜索模型" placeholder="搜索模型或提供商" value={search} onChange={event => setSearch(event.currentTarget.value)} />
        <div className="session-model-options">
          {options.isFetching && <Text size="sm" c="dimmed">读取可用模型…</Text>}
          {options.data?.items.filter(item => item.label.toLocaleLowerCase().includes(search.toLocaleLowerCase())).map(item => <UnstyledButton key={JSON.stringify([item.provider, item.model])} className="session-model-option" aria-pressed={choice?.provider === item.provider && choice?.model === item.model} onClick={() => setChoice(item)}>
            <IconCpu size={17} /><span>{item.label}</span>{choice?.provider === item.provider && choice?.model === item.model && <IconCheck size={17} />}
          </UnstyledButton>)}
        </div>
        {options.isError && <Button variant="subtle" onClick={() => void options.refetch()}>重新读取可用模型</Button>}
        <Group justify="flex-end"><Button variant="default" onClick={() => setOpened(false)}>取消</Button><Button aria-label="确认切换模型" disabled={!choice} loading={changing} onClick={() => void apply()}>确认切换模型</Button></Group>
      </Stack>
    </Modal>
  </>
}
