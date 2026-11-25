import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowBackUp,
  IconArrowForwardUp,
  IconBlockquote,
  IconBold,
  IconCheck,
  IconItalic,
  IconList,
  IconListNumbers,
  IconMessageForward,
  IconSeparator,
  IconSparkles,
  IconStrikethrough,
  IconTablePlus,
  IconUnderline,
  IconX,
} from '@tabler/icons-react'
import { api } from '../../../../api/client'
import type { Session } from '../../../../domain/types'
import { diffDocxParagraphs } from '../docx/docxDiff'
import { countDocumentStats, markdownToRichHtml, richHtmlToMarkdown } from './markdownHtmlCodec'
import './richDocumentEditor.css'

const SELECTION_AI_PRESETS = [
  { label: '精简', instruction: '压缩篇幅 35%，保留核心事实与关键数字，语言更精炼。' },
  { label: '扩写', instruction: '补充清晰的背景、执行要点与量化支撑，逻辑连贯，不编造数据。' },
  { label: '更正式', instruction: '改写为面向管理层的正式书面汇报口吻，结论先行。' },
  { label: '润色', instruction: '优化语句通顺度与专业表达，消除口语化和重复表述。' },
  { label: '译英文', instruction: '翻译为地道、专业的商务英文表达。' },
  { label: '脱敏', instruction: '隐去内部地址、路径、账号和敏感代号，改为可对外发送的表述。' },
]

export interface RichDocumentEditorProps {
  markdown: string
  documentTitle: string
  session: Session | null
  writable: boolean
  onChange: (nextMarkdown: string) => void
  onSaveNow?: (nextMarkdown: string) => Promise<boolean>
}

export function RichDocumentEditor({
  markdown,
  documentTitle,
  session,
  writable,
  onChange,
  onSaveNow,
}: RichDocumentEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const lastEmittedMdRef = useRef<string | null>(null)
  const savedRangeRef = useRef<Range | null>(null)

  const [selectedText, setSelectedText] = useState('')
  const [customInstruction, setCustomInstruction] = useState('')
  const [refining, setRefining] = useState(false)
  const [lastUndoMarkdown, setLastUndoMarkdown] = useState<string | null>(null)
  const [candidate, setCandidate] = useState<{
    original: string
    rewritten: string
    mode: string
    model?: string
    assembledPrompt: string
    message?: string
  } | null>(null)

  // Hydrate contentEditable only when external markdown prop actually changes
  useEffect(() => {
    const el = editorRef.current
    if (!el) return
    if (lastEmittedMdRef.current === markdown) return
    el.innerHTML = markdownToRichHtml(markdown)
    lastEmittedMdRef.current = markdown
  }, [markdown])

  const stats = useMemo(() => countDocumentStats(markdown), [markdown])

  const emitCurrentMarkdown = useCallback(() => {
    const el = editorRef.current
    if (!el) return
    const nextMd = richHtmlToMarkdown(el.innerHTML)
    lastEmittedMdRef.current = nextMd
    onChange(nextMd)
    return nextMd
  }, [onChange])

  const execFormat = (command: string, value?: string) => {
    if (!writable) return
    editorRef.current?.focus()
    try {
      document.execCommand(command, false, value)
    } catch {
      // ignore unsupported execCommand in synthetic DOM
    }
    emitCurrentMarkdown()
  }

  const insertTable = () => {
    if (!writable) return
    editorRef.current?.focus()
    const html =
      '<table><thead><tr><th>项目 / 维度</th><th>当前进展</th><th>备注</th></tr></thead>' +
      '<tbody><tr><td>条目一</td><td>进行中</td><td>—</td></tr>' +
      '<tr><td>条目二</td><td>待启动</td><td>—</td></tr></tbody></table><p><br /></p>'
    try {
      document.execCommand('insertHTML', false, html)
    } catch {
      if (editorRef.current) editorRef.current.innerHTML += html
    }
    emitCurrentMarkdown()
  }

  const captureSelection = () => {
    const el = editorRef.current
    if (!el) return
    const sel = window.getSelection()
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      if (!candidate && !refining) {
        setSelectedText('')
        savedRangeRef.current = null
      }
      return
    }
    const range = sel.getRangeAt(0)
    if (!el.contains(range.commonAncestorContainer)) return
    const text = sel.toString().trim()
    if (text.length >= 2) {
      setSelectedText(text)
      savedRangeRef.current = range.cloneRange()
    } else if (!candidate && !refining) {
      setSelectedText('')
      savedRangeRef.current = null
    }
  }

  const runSelectionRefine = async (instr?: string) => {
    const reqInstruction = (instr ?? customInstruction).trim()
    if (!selectedText.trim() || !reqInstruction) return
    setRefining(true)
    try {
      const resp = await api.refineOfficeSelection({
        document_title: documentTitle,
        selected_text: selectedText,
        instruction: reqInstruction,
      })
      setCandidate({
        original: selectedText,
        rewritten: resp.rewritten_text.trim(),
        mode: resp.mode,
        model: resp.model,
        assembledPrompt: resp.assembled_prompt,
        message: resp.message,
      })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : 'AI 改写失败',
      })
    } finally {
      setRefining(false)
    }
  }

  const applySelectionCandidate = async (mode: 'replace' | 'insert_below') => {
    if (!candidate || !writable) return
    const el = editorRef.current
    if (!el) return
    const prevMd = richHtmlToMarkdown(el.innerHTML)
    setLastUndoMarkdown(prevMd)

    let nextMd: string
    const range = savedRangeRef.current
    if (range && el.contains(range.commonAncestorContainer)) {
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
      if (mode === 'replace') {
        range.deleteContents()
        const frag = document.createElement('span')
        frag.textContent = candidate.rewritten
        range.insertNode(frag)
      } else {
        range.collapse(false)
        const p = document.createElement('p')
        p.textContent = candidate.rewritten
        range.insertNode(p)
      }
      nextMd = emitCurrentMarkdown() || prevMd
    } else {
      nextMd =
        mode === 'replace'
          ? prevMd.replace(candidate.original, candidate.rewritten)
          : `${prevMd}\n\n${candidate.rewritten}`
      el.innerHTML = markdownToRichHtml(nextMd)
      lastEmittedMdRef.current = nextMd
      onChange(nextMd)
    }

    setCandidate(null)
    setSelectedText('')
    setCustomInstruction('')
    savedRangeRef.current = null

    if (onSaveNow) {
      const saved = await onSaveNow(nextMd)
      notifications.show({
        color: 'teal',
        message: saved ? '已应用 AI 修改并自动保存' : '已应用 AI 修改',
      })
    } else {
      notifications.show({ color: 'teal', message: '已应用 AI 修改' })
    }
  }

  const handleUndoAi = async () => {
    if (lastUndoMarkdown === null) return
    const restore = lastUndoMarkdown
    setLastUndoMarkdown(null)
    if (editorRef.current) {
      editorRef.current.innerHTML = markdownToRichHtml(restore)
    }
    lastEmittedMdRef.current = restore
    onChange(restore)
    if (onSaveNow) await onSaveNow(restore)
    notifications.show({ color: 'teal', message: '已撤销上次 AI 修改' })
  }

  const diffPreview = useMemo(() => {
    if (!candidate || candidate.mode !== 'llm') return null
    return diffDocxParagraphs(candidate.original.split('\n'), candidate.rewritten.split('\n'))
  }, [candidate])

  return (
    <div className="rich-doc-studio" aria-label={`富文档写作台：${documentTitle}`}>
      {/* Clean WYSIWYG Formatting Toolbar */}
      <div className="rich-doc-toolbar" role="toolbar" aria-label="文档排版工具栏">
        <div className="rich-doc-toolbar-left">
          <select
            className="rich-doc-block-select"
            aria-label="段落层级"
            disabled={!writable}
            defaultValue="P"
            onChange={(e) => {
              execFormat('formatBlock', e.currentTarget.value)
            }}
          >
            <option value="P">正文</option>
            <option value="H1">一级标题</option>
            <option value="H2">二级标题</option>
            <option value="H3">三级标题</option>
          </select>

          <span className="rich-doc-sep" aria-hidden="true" />

          <Tooltip label="加粗 (Ctrl+B)">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="加粗" onClick={() => execFormat('bold')}>
              <IconBold size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="斜体 (Ctrl+I)">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="斜体" onClick={() => execFormat('italic')}>
              <IconItalic size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="下划线">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="下划线" onClick={() => execFormat('underline')}>
              <IconUnderline size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="删除线">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="删除线" onClick={() => execFormat('strikeThrough')}>
              <IconStrikethrough size={14} />
            </ActionIcon>
          </Tooltip>

          <span className="rich-doc-sep" aria-hidden="true" />

          <Tooltip label="无序列表">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="无序列表" onClick={() => execFormat('insertUnorderedList')}>
              <IconList size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="有序列表">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="有序列表" onClick={() => execFormat('insertOrderedList')}>
              <IconListNumbers size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="引用块">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="引用块" onClick={() => execFormat('formatBlock', 'BLOCKQUOTE')}>
              <IconBlockquote size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="插入表格">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="插入表格" onClick={insertTable}>
              <IconTablePlus size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="插入分割线">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="插入分割线" onClick={() => execFormat('insertHorizontalRule')}>
              <IconSeparator size={14} />
            </ActionIcon>
          </Tooltip>

          <span className="rich-doc-sep" aria-hidden="true" />

          <Tooltip label="撤销">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="撤销" onClick={() => execFormat('undo')}>
              <IconArrowBackUp size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="重做">
            <ActionIcon size="sm" variant="subtle" color="gray" disabled={!writable} aria-label="重做" onClick={() => execFormat('redo')}>
              <IconArrowForwardUp size={14} />
            </ActionIcon>
          </Tooltip>
        </div>

        <div className="rich-doc-toolbar-right">
          {lastUndoMarkdown !== null && (
            <Button
              size="compact-xs"
              variant="subtle"
              color="gray"
              leftSection={<IconArrowBackUp size={12} />}
              onClick={() => void handleUndoAi()}
            >
              撤销 AI 修改
            </Button>
          )}
          <span className="rich-doc-stats">
            {stats.words} 字{stats.minutes > 0 ? ` · 约 ${stats.minutes} 分钟` : ''}
          </span>
        </div>
      </div>

      {/* Scrollable Paper Surface */}
      <div className="rich-doc-scroll">
        {/* Floating AI Selection Bar */}
        {(selectedText || candidate) && (
          <div className="rich-ai-bar" role="region" aria-label="划词 AI 写作助手">
            <Group justify="space-between" gap={6} wrap="nowrap">
              <Group gap={6} wrap="wrap">
                <span className="rich-ai-badge">
                  <IconSparkles size={12} />
                  <span>AI 划词精修</span>
                </span>
                {SELECTION_AI_PRESETS.map((p) => (
                  <button
                    key={p.label}
                    type="button"
                    className="rich-ai-pill"
                    disabled={refining}
                    onClick={() => {
                      setCustomInstruction(p.instruction)
                      void runSelectionRefine(p.instruction)
                    }}
                  >
                    {p.label}
                  </button>
                ))}
              </Group>
              <ActionIcon
                size="xs"
                variant="subtle"
                color="gray"
                aria-label="关闭划词工具栏"
                onClick={() => {
                  setSelectedText('')
                  setCandidate(null)
                  savedRangeRef.current = null
                }}
              >
                <IconX size={13} />
              </ActionIcon>
            </Group>

            <Group gap={6} mt={6} wrap="nowrap">
              <TextInput
                size="xs"
                style={{ flex: 1 }}
                placeholder="输入改写要求，如：语气更委婉、拆为三条要点…"
                value={customInstruction}
                onChange={(e) => setCustomInstruction(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && customInstruction.trim()) {
                    e.preventDefault()
                    void runSelectionRefine(customInstruction)
                  }
                }}
              />
              <Button
                size="xs"
                color="dark"
                loading={refining}
                disabled={!customInstruction.trim()}
                leftSection={<IconSparkles size={12} />}
                onClick={() => void runSelectionRefine(customInstruction)}
              >
                改写
              </Button>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconMessageForward size={12} />}
                onClick={() => {
                  const text = `请帮我改写文档《${documentTitle}》中的以下选段（要求：${customInstruction.trim() || '专业润色'}）：\n\n> ${selectedText.replace(/\n/g, '\n> ')}`
                  window.dispatchEvent(
                    new CustomEvent('astrorder:quote-to-composer', {
                      detail: { agentId: session?.agent_id, sessionId: session?.id, text },
                    }),
                  )
                  notifications.show({ color: 'teal', message: '已发送到对话输入框' })
                }}
              >
                发给对话
              </Button>
            </Group>

            {candidate && (
              <div className="rich-ai-preview">
                {candidate.mode === 'prompt_only' ? (
                  <Group justify="space-between" gap="xs">
                    <Text size="xs" c="dimmed">
                      {candidate.message || '未配置 AI 供应商模型，可直接发给对话处理。'}
                    </Text>
                    <Button
                      size="compact-xs"
                      color="dark"
                      onClick={() => {
                        window.dispatchEvent(
                          new CustomEvent('astrorder:quote-to-composer', {
                            detail: {
                              agentId: session?.agent_id,
                              sessionId: session?.id,
                              text: candidate.assembledPrompt,
                            },
                          }),
                        )
                        notifications.show({ color: 'teal', message: '已发送完整提示词到对话' })
                      }}
                    >
                      发给对话执行
                    </Button>
                  </Group>
                ) : (
                  <>
                    <Group justify="space-between" mb={6}>
                      <Group gap={6}>
                        <Badge size="xs" variant="light" color="teal">AI 建议</Badge>
                        {candidate.model && <Text size="11px" c="dimmed">{candidate.model}</Text>}
                      </Group>
                      <Group gap={6}>
                        <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setCandidate(null)}>
                          放弃
                        </Button>
                        <Button
                          size="compact-xs"
                          variant="default"
                          disabled={!writable}
                          onClick={() => void applySelectionCandidate('insert_below')}
                        >
                          插入下方
                        </Button>
                        <Button
                          size="compact-xs"
                          color="dark"
                          disabled={!writable}
                          leftSection={<IconCheck size={12} />}
                          onClick={() => void applySelectionCandidate('replace')}
                        >
                          替换选中
                        </Button>
                      </Group>
                    </Group>
                    {diffPreview && (
                      <div className="rich-ai-diff">
                        {diffPreview.entries.map((entry, idx) => (
                          <div key={idx} className={`rich-ai-diff-line is-${entry.kind}`}>
                            {entry.kind === 'removed' ? '- ' : entry.kind === 'added' ? '+ ' : '  '}
                            {entry.text || ' '}
                          </div>
                        ))}
                      </div>
                    )}
                  </>
                )}
              </div>
            )}
          </div>
        )}

        <div className="rich-doc-paper-wrap">
          <div
            ref={editorRef}
            className="rich-doc-paper"
            contentEditable={writable}
            suppressContentEditableWarning
            role="textbox"
            aria-multiline="true"
            aria-label="富文档正文编辑区"
            data-placeholder="开始撰写文档内容，或划选任意段落唤起 AI 精修…"
            onInput={() => emitCurrentMarkdown()}
            onMouseUp={captureSelection}
            onKeyUp={captureSelection}
          />
        </div>
      </div>
    </div>
  )
}
