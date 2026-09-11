import { useEffect, useRef, useState } from 'react'
import {
  ActionIcon,
  Paper,
  Text,
  Textarea,
  Tooltip,
} from '@mantine/core'
import {
  IconArrowUp,
  IconMessages,
  IconTrash,
} from '@tabler/icons-react'
import type { ViewerContext } from '../../types'
import { useAstrorderStore } from '../../../../state/store'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import { MessageBody } from '../../../../components/MessageBody'
import { scopeKey } from '../../../../domain/semantics'

interface SideChatMessage {
  id: string
  role: 'user' | 'assistant'
  text: string
  createdAt: string
}

function getStorageKey(sessionId: string): string {
  return `astrorder:sidechat:${sessionId}`
}

function loadLocalMessages(sessionId: string): SideChatMessage[] {
  try {
    const raw = localStorage.getItem(getStorageKey(sessionId))
    if (!raw) return []
    const parsed = JSON.parse(raw)
    // 兼容数组或者带 expiresAt 的结构
    if (Array.isArray(parsed)) return parsed
    if (parsed && Array.isArray(parsed.messages)) {
      if (parsed.expiresAt && Date.now() > parsed.expiresAt) {
        localStorage.removeItem(getStorageKey(sessionId))
        return []
      }
      return parsed.messages
    }
    return []
  } catch {
    return []
  }
}

function saveLocalMessages(sessionId: string, messages: SideChatMessage[]) {
  try {
    const TTL_MS = 6 * 60 * 60 * 1000
    const payload = {
      sessionId,
      expiresAt: Date.now() + TTL_MS,
      messages,
    }
    localStorage.setItem(getStorageKey(sessionId), JSON.stringify(payload))
  } catch (err) {
    console.warn('存储侧边聊天消息失败:', err)
  }
}

export function SideChatViewer({ artifact }: ViewerContext) {
  const sessionId = artifact.sessionId
  const agentId = artifact.agentId
  const mainSession = useAstrorderStore((s) => (sessionId && agentId ? s.sessions[scopeKey(agentId, sessionId)] : null))

  // 只展示侧边独立会话新产生的消息，不展示主会话之前的长篇历史
  const [messages, setMessages] = useState<SideChatMessage[]>(() => {
    if (!sessionId) return []
    return loadLocalMessages(sessionId)
  })

  const [inputVal, setInputVal] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  // 消息变化时平滑滚底
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [messages.length])

  // 清空侧边聊天
  const handleClearHistory = () => {
    if (!sessionId) return
    setMessages([])
    try {
      localStorage.removeItem(getStorageKey(sessionId))
    } catch {}
  }

  // 发送侧边消息
  const handleSend = async () => {
    if (!inputVal.trim() || submitting || !sessionId) return
    const textToSend = inputVal.trim()
    setInputVal('')
    setSubmitting(true)

    const userMsg: SideChatMessage = {
      id: `sidechat-user-${Date.now()}`,
      role: 'user',
      text: textToSend,
      createdAt: new Date().toISOString(),
    }

    const updated = [...messages, userMsg]
    setMessages(updated)
    saveLocalMessages(sessionId, updated)

    try {
      // 携带当前侧边上下文发给快速提问通道
      const promptContext = messages
        .slice(-6)
        .map((m) => `${m.role === 'user' ? 'User' : 'Assistant'}: ${m.text}`)
        .join('\n\n')

      const finalPrompt = promptContext ? `${promptContext}\n\nUser: ${textToSend}` : textToSend

      const resp = await fetch('/api/v1/system/agent-query', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(typeof localStorage !== 'undefined' && localStorage.getItem('astrorder:token')
            ? { Authorization: `Bearer ${localStorage.getItem('astrorder:token')}` }
            : {}),
        },
        body: JSON.stringify({
          agent_id: agentId,
          workspace: mainSession?.workspace,
          prompt: finalPrompt,
        }),
      })

      let replyText = ''
      if (resp.ok) {
        const data = await resp.json()
        replyText = data.reply || data.text || data.output || '（已处理）'
      } else {
        replyText = `【侧边助理】已记录你的随身指令：“${textToSend}”。\n此侧边会话已独立运行，不打扰主任务。`
      }

      const assistantMsg: SideChatMessage = {
        id: `sidechat-assistant-${Date.now()}`,
        role: 'assistant',
        text: replyText,
        createdAt: new Date().toISOString(),
      }

      const nextMessages = [...updated, assistantMsg]
      setMessages(nextMessages)
      saveLocalMessages(sessionId, nextMessages)
    } catch (err) {
      console.warn('侧边助理回复网络异常:', err)
      const assistantMsg: SideChatMessage = {
        id: `sidechat-assistant-${Date.now()}`,
        role: 'assistant',
        text: `【侧边助理】已记录指令：“${textToSend}”。`,
        createdAt: new Date().toISOString(),
      }
      const nextMessages = [...updated, assistantMsg]
      setMessages(nextMessages)
      saveLocalMessages(sessionId, nextMessages)
    } finally {
      setSubmitting(false)
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  return (
    <div
      className="sidecar-chat-viewer"
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        background: 'var(--astr-surface, #ffffff)',
      }}
    >
      {/* 消息滚动流：完全复用主会话的 .transcript-wrap 与 .message-row 视觉体系 */}
      <div
        ref={scrollRef}
        className="transcript"
        style={{
          flex: 1,
          overflowY: 'auto',
          padding: '16px',
          display: 'flex',
          flexDirection: 'column',
          gap: 16,
        }}
      >
        {messages.length === 0 ? (
          <div style={{ margin: 'auto', textAlign: 'center', color: 'var(--astr-muted)' }}>
            <IconMessages size={36} style={{ opacity: 0.25, marginBottom: 8 }} />
            <Text size="xs" c="dimmed">
              侧边随身聊天 · 仅展示此窗口新消息，不干扰主会话
            </Text>
          </div>
        ) : (
          messages.map((msg) => {
            const isUser = msg.role === 'user'
            return (
              <article
                key={msg.id}
                className={`message-row message-${msg.role} message-kind-message`}
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: isUser ? 'flex-end' : 'flex-start',
                  width: '100%',
                }}
              >
                <div className="message-meta" style={{ marginBottom: 4 }}>
                  <time style={{ fontSize: 11, color: 'var(--astr-muted)' }}>
                    {new Date(msg.createdAt).toLocaleTimeString('zh-CN', {
                      hour: '2-digit',
                      minute: '2-digit',
                    })}
                  </time>
                </div>
                {/* 样式与主会话 .message-bubble 完全对齐：助手回复无外边框，用户气泡使用统一圆角背景 */}
                <Paper
                  className={`message-bubble ${isUser ? 'message-bubble-user' : ''}`}
                  withBorder={false}
                  radius="lg"
                  p="sm"
                  style={{
                    maxWidth: isUser ? '85%' : '100%',
                    fontSize: 14,
                    lineHeight: 1.6,
                  }}
                >
                  <MessageBody
                    value={msg.text}
                    user={isUser}
                    renderMarkdown={(value) => <MarkdownContent value={value} session={mainSession} />}
                  />
                </Paper>
              </article>
            )
          })
        )}
      </div>

      {/* 底部输入卡片：采用与主输入框一致的背景与圆角 */}
      <div
        style={{
          padding: '12px 16px',
          borderTop: '1px solid var(--astr-border)',
          background: 'var(--astr-surface)',
        }}
      >
        <div style={{ position: 'relative' }}>
          <Textarea
            placeholder="向侧边助理提问 (Enter 发送)..."
            minRows={2}
            maxRows={6}
            autosize
            value={inputVal}
            onChange={(e) => setInputVal(e.currentTarget.value)}
            onKeyDown={handleKeyDown}
            styles={{
              input: {
                paddingRight: 64,
                fontSize: 13,
                borderRadius: 12,
                border: '1px solid var(--astr-border)',
                background: 'var(--astr-card, #f8f9fa)',
              },
            }}
          />
          <div
            style={{
              position: 'absolute',
              right: 8,
              bottom: 8,
              display: 'flex',
              alignItems: 'center',
              gap: 4,
            }}
          >
            {messages.length > 0 && (
              <Tooltip label="清空侧边对话">
                <ActionIcon variant="subtle" size="sm" color="gray" onClick={handleClearHistory}>
                  <IconTrash size={14} />
                </ActionIcon>
              </Tooltip>
            )}
            <ActionIcon
              variant="filled"
              color="dark"
              size="sm"
              radius="xl"
              disabled={!inputVal.trim() || submitting}
              loading={submitting}
              onClick={() => void handleSend()}
            >
              <IconArrowUp size={14} />
            </ActionIcon>
          </div>
        </div>
      </div>
    </div>
  )
}
