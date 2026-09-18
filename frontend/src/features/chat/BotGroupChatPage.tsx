import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Paper,
  Stack,
  Text,
  Textarea,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAt,
  IconPlayerStop,
  IconSend,
  IconUsers,
  IconX,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import type { Agent, BotGroup, GroupMessage } from '../../domain/types'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'

function senderLabel(msg: GroupMessage, members: BotGroup['members'], agents: Record<string, Agent>): string {
  if (msg.sender_type === 'user') return '用户 (指挥官)'
  const found = members.find(m => m.agent_id === msg.sender_id)
  if (found?.alias) return `${found.alias} (${found.name || msg.sender_id})`
  const ag = agents[msg.sender_id]
  return ag?.name || found?.name || msg.sender_id
}

export function BotGroupChatPage({
  group,
  agents,
  onClose,
}: {
  group: BotGroup
  agents: Record<string, Agent>
  onClose?: () => void
}) {
  const [messages, setMessages] = useState<GroupMessage[]>([])
  const [input, setInput] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [activeHop, setActiveHop] = useState(group.active_hop || 0)
  const [activeSpeaker, setActiveSpeaker] = useState<string | null>(group.active_speaker_agent_id || null)
  const [activeSpeakers, setActiveSpeakers] = useState<string[]>(group.active_speakers || [])
  const [mentionMenuOpen, setMentionMenuOpen] = useState(false)
  const [mentionQuery, setMentionQuery] = useState('')
  const [mentionIndex, setMentionIndex] = useState(0)
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const loadMessages = async () => {
    try {
      const [resMsgs, resGroup] = await Promise.all([
        api.listGroupMessages(group.id),
        api.getBotGroup(group.id),
      ])
      setMessages(resMsgs.items)
      setActiveHop(resGroup.group.active_hop || 0)
      setActiveSpeaker(resGroup.group.active_speaker_agent_id || null)
      setActiveSpeakers(resGroup.group.active_speakers || (resGroup.group.active_speaker_agent_id ? [resGroup.group.active_speaker_agent_id] : []))
    } catch (err) {
      console.error('Failed to load group messages', err)
    }
  }

  useEffect(() => {
    void loadMessages()
    const timer = setInterval(() => void loadMessages(), 3000)
    return () => clearInterval(timer)
  }, [group.id])

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [messages.length])

  // Mention items including @all
  const mentionItems = [
    { id: '__all__', name: 'all', label: '所有人 (@all)', desc: '全员协同广播', kind: undefined as string | undefined, isAll: true },
    ...group.members.map(m => {
      const ag = agents[m.agent_id]
      return {
        id: m.agent_id,
        name: m.name || m.agent_id,
        label: `${m.name || m.agent_id}${m.alias ? ' · ' + m.alias : ''}`,
        desc: ag ? `${ag.name || ag.id} (${m.machine_id === 'local' ? '本机' : m.machine_id})` : m.machine_id,
        kind: ag?.kind,
        isAll: false,
      }
    }),
  ].filter(item => {
    if (!mentionQuery) return true
    const q = mentionQuery.toLowerCase()
    return item.name.toLowerCase().includes(q) || item.label.toLowerCase().includes(q) || item.desc.toLowerCase().includes(q)
  })

  const checkMentionTrigger = (val: string, cursor: number) => {
    const textBefore = val.slice(0, cursor)
    const match = textBefore.match(/@([^\s@]*)$/)
    if (match) {
      setMentionMenuOpen(true)
      setMentionQuery(match[1])
      setMentionIndex(0)
    } else {
      setMentionMenuOpen(false)
      setMentionQuery('')
    }
  }

  const selectMention = (item: typeof mentionItems[0]) => {
    const el = textareaRef.current
    const cursor = el?.selectionStart ?? input.length
    const textBefore = input.slice(0, cursor)
    const textAfter = input.slice(cursor)
    const match = textBefore.match(/@([^\s@]*)$/)
    const mentionTag = item.isAll ? '@all ' : `@${item.name} `
    if (match) {
      const start = match.index ?? (cursor - match[0].length)
      const nextText = input.slice(0, start) + mentionTag + textAfter
      setInput(nextText)
      setTimeout(() => {
        if (el) {
          el.focus()
          const nextPos = start + mentionTag.length
          el.setSelectionRange(nextPos, nextPos)
        }
      }, 20)
    } else {
      setInput(prev => `${prev}${mentionTag}`)
    }
    setMentionMenuOpen(false)
    setMentionQuery('')
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionMenuOpen && mentionItems.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setMentionIndex(prev => (prev + 1) % mentionItems.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setMentionIndex(prev => (prev - 1 + mentionItems.length) % mentionItems.length)
        return
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault()
        selectMention(mentionItems[mentionIndex] || mentionItems[0])
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setMentionMenuOpen(false)
        return
      }
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send()
    }
  }

  const send = async () => {
    if (submitting || !input.trim()) return
    setSubmitting(true)
    setMentionMenuOpen(false)
    try {
      const text = input.trim()
      setInput('')
      const res = await api.sendGroupMessage(group.id, { text })
      setMessages(prev => [...prev, res.message])
      if (res.target_agent_id) {
        setActiveSpeaker(res.target_agent_id)
        setActiveHop(1)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '发送群消息失败' })
    } finally {
      setSubmitting(false)
    }
  }

  const stopGroup = async () => {
    try {
      await api.stopGroup(group.id)
      setActiveSpeaker(null)
      setActiveHop(0)
      notifications.show({ color: 'yellow', message: '已中断群聊接力流转' })
    } catch (err) {
      notifications.show({ color: 'red', message: '中断失败' })
    }
  }

  const insertMentionDirect = (name: string) => {
    setInput(prev => `${prev}@${name} `)
    textareaRef.current?.focus()
  }

  return (
    <div className="bot-group-chat-page" style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <Paper p="sm" withBorder style={{ borderTop: 0, borderLeft: 0, borderRight: 0, borderRadius: 0 }}>
        <Group justify="space-between" align="center" wrap="nowrap">
          <Group gap="sm" wrap="nowrap" align="center">
            <IconUsers size={20} color="var(--mantine-color-indigo-5)" />
            <div>
              <Group gap={6} align="center">
                <Text fw={600} size="md">{group.name}</Text>
                <Badge size="xs" variant="light" color="indigo">群聊 ({group.members.length} 人)</Badge>
                {activeSpeaker && (
                  <Badge size="xs" color="teal" variant="filled">
                    接力流转中: {activeHop}/{group.max_hops}
                  </Badge>
                )}
              </Group>
              {group.description && (
                <Text size="xs" c="dimmed" lineClamp={1}>{group.description}</Text>
              )}
            </div>
          </Group>

          <Group gap="xs" wrap="nowrap">
            {activeSpeaker && (
              <Button
                size="compact-xs"
                color="red"
                variant="light"
                leftSection={<IconPlayerStop size={13} />}
                onClick={() => void stopGroup()}
              >
                中断流转
              </Button>
            )}
            {onClose && (
              <ActionIcon variant="subtle" color="gray" onClick={onClose}>
                <IconX size={16} />
              </ActionIcon>
            )}
          </Group>
        </Group>

        <Group gap={6} mt={8} wrap="nowrap" style={{ overflowX: 'auto' }}>
          <Badge
            size="sm"
            variant="light"
            color="indigo"
            style={{ cursor: 'pointer', textTransform: 'none' }}
            onClick={() => insertMentionDirect('all')}
            leftSection={<IconAt size={12} />}
            title="点击输入 @all 全员广播"
          >
            all (全员)
          </Badge>
          {group.members.map(m => {
            const ag = agents[m.agent_id]
            const isSpeaking = activeSpeakers.includes(m.agent_id) || activeSpeaker === m.agent_id
            return (
              <Badge
                key={m.agent_id}
                size="sm"
                variant={isSpeaking ? 'filled' : 'outline'}
                color={isSpeaking ? 'teal' : 'gray'}
                style={{ cursor: 'pointer', textTransform: 'none' }}
                onClick={() => insertMentionDirect(m.name || m.agent_id)}
                title="点击在输入框 @TA"
                leftSection={ag ? <AgentBrandIcon kind={ag.kind} size={12} /> : undefined}
              >
                {m.name || m.agent_id}{m.alias ? ' · ' + m.alias : ''}
              </Badge>
            )
          })}
        </Group>
      </Paper>

      <div ref={scrollRef} style={{ flex: 1, overflowY: 'auto', padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 14 }}>
        {messages.length === 0 && (
          <div style={{ margin: 'auto', textAlign: 'center', opacity: 0.6 }}>
            <IconUsers size={36} style={{ margin: '0 auto 8px', display: 'block' }} />
            <Text size="sm">群聊已就绪。在下方输入框键入 @ 即可选择特定成员或 @all 进行广播协作。</Text>
            <Text size="xs" c="dimmed" mt={4}>
              规则：Agent 回复若未包含 @其它成员 则自动静默闭嘴；单次最多接力 {group.max_hops} 轮。
            </Text>
          </div>
        )}

        {messages.map(m => {
          const isUser = m.sender_type === 'user'
          const label = senderLabel(m, group.members, agents)
          const ag = agents[m.sender_id]
          return (
            <div key={m.id} style={{ alignSelf: isUser ? 'flex-end' : 'flex-start', maxWidth: '82%' }}>
              <Group gap={6} align="center" mb={2} justify={isUser ? 'flex-end' : 'flex-start'}>
                {!isUser && ag && <AgentBrandIcon kind={ag.kind} size={13} />}
                <Text size="xs" fw={500} c={isUser ? 'indigo' : 'dimmed'}>{label}</Text>
                {m.hop_count > 0 && <Badge size="xs" variant="dot" color="orange">第 {m.hop_count} 跳</Badge>}
              </Group>
              <Paper p="sm" radius="md" withBorder style={{ background: isUser ? 'color-mix(in srgb, var(--mantine-color-indigo-6) 12%, var(--astr-surface, #fff))' : 'var(--astr-surface, #fff)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: 14, lineHeight: 1.55 }}>
                {m.text}
              </Paper>
            </div>
          )
        })}
      </div>

      <Paper p="sm" withBorder style={{ borderBottom: 0, borderLeft: 0, borderRight: 0, borderRadius: 0, position: 'relative' }}>
        {mentionMenuOpen && mentionItems.length > 0 && (
          <Paper withBorder shadow="lg" radius="md" p={4} role="listbox" aria-label="群成员建议" style={{ position: 'absolute', zIndex: 50, left: 12, right: 12, bottom: 'calc(100% + 8px)', maxHeight: 220, overflowY: 'auto', background: 'var(--astr-surface, #fff)' }}>
            <Stack gap={2}>
              {mentionItems.map((item, idx) => {
                const isSelected = idx === mentionIndex
                return (
                  <UnstyledButton key={item.id} p="xs" style={{ borderRadius: 6, background: isSelected ? 'var(--mantine-color-indigo-light)' : undefined }} onMouseDown={(e) => { e.preventDefault(); selectMention(item) }}>
                    <Group gap="xs" wrap="nowrap">
                      {item.isAll ? (
                        <ActionIcon size="sm" variant="light" color="indigo" radius="xl">
                          <IconUsers size={14} />
                        </ActionIcon>
                      ) : (
                        item.kind ? <AgentBrandIcon kind={item.kind} size={18} /> : <IconAt size={16} />
                      )}
                      <div style={{ minWidth: 0, flex: 1 }}>
                        <Text size="sm" fw={600}>@{item.name}</Text>
                        <Text size="xs" c="dimmed" lineClamp={1}>{item.desc}</Text>
                      </div>
                      {item.isAll && <Badge size="xs" color="indigo">全员</Badge>}
                    </Group>
                  </UnstyledButton>
                )
              })}
            </Stack>
          </Paper>
        )}

        <Stack gap="xs">
          <Textarea ref={textareaRef} placeholder="输入消息，键入 @ 可呼出群成员列表或 @all…" minRows={2} maxRows={6} autosize value={input} onChange={(e) => { const val = e.currentTarget.value; setInput(val); checkMentionTrigger(val, e.currentTarget.selectionStart) }} onKeyDown={handleKeyDown} disabled={submitting} />
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed">提示：输入 @ 即可定向指定 Agent 或 @all 广播</Text>
            <Button size="xs" color="indigo" rightSection={<IconSend size={14} />} onClick={() => void send()} loading={submitting} disabled={!input.trim()}>
              发送
            </Button>
          </Group>
        </Stack>
      </Paper>
    </div>
  )
}
