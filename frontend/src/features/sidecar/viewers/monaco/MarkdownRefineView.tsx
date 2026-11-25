import { useEffect, useMemo, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Select, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowBackUp,
  IconCheck,
  IconLayoutSidebar,
  IconMessageForward,
  IconSparkles,
  IconX,
} from '@tabler/icons-react'
import { api, type AiProvider } from '../../../../api/client'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import type { Session } from '../../../../domain/types'
import { diffDocxParagraphs } from '../docx/docxDiff'
import './markdownRefine.css'

export interface MarkdownSection {
  id: string
  heading?: string
  level?: number
  startLine: number
  endLine: number // exclusive
  markdown: string
}

/**
 * Splits a Markdown document into line-indexed sections along heading boundaries
 * while keeping fenced code blocks intact, enabling lossless section replacement.
 * Falls back to paragraph-group chunking for headingless long documents.
 */
export function splitMarkdownSections(markdown: string): MarkdownSection[] {
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')
  const sections: MarkdownSection[] = []
  let inFence = false
  let curStart = 0
  let curHeading: string | undefined
  let curLevel: number | undefined

  const pushSlice = (endIdx: number) => {
    const slice = lines.slice(curStart, endIdx).join('\n')
    if (slice.trim() || sections.length === 0) {
      sections.push({
        id: `sec-${curStart}`,
        heading: curHeading,
        level: curLevel,
        startLine: curStart,
        endLine: endIdx,
        markdown: slice,
      })
    }
  }

  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim()
    if (/^(`{3,}|~{3,})/.test(trimmed)) {
      inFence = !inFence
      continue
    }
    if (inFence) continue
    const headingMatch = trimmed.match(/^(#{1,4})\s+(.+)$/)
    if (headingMatch) {
      if (i > curStart) {
        pushSlice(i)
      }
      curStart = i
      curLevel = headingMatch[1].length
      curHeading = headingMatch[2].trim()
    }
  }
  pushSlice(lines.length)

  // Headingless long documents: chunk along blank-line boundaries (~8-12 lines per block)
  if (sections.length === 1 && !sections[0].heading && lines.length > 18) {
    const chunks: MarkdownSection[] = []
    let chunkStart = 0
    let fenceOpen = false
    for (let i = 0; i < lines.length; i++) {
      const trimmed = lines[i].trim()
      if (/^(`{3,}|~{3,})/.test(trimmed)) {
        fenceOpen = !fenceOpen
        continue
      }
      if (fenceOpen) continue
      if (trimmed === '' && i - chunkStart >= 8) {
        const slice = lines.slice(chunkStart, i).join('\n')
        if (slice.trim()) {
          chunks.push({
            id: `sec-${chunkStart}`,
            startLine: chunkStart,
            endLine: i,
            markdown: slice,
          })
          chunkStart = i + 1
        }
      }
    }
    if (chunkStart < lines.length) {
      const tail = lines.slice(chunkStart).join('\n')
      if (tail.trim() || chunks.length === 0) {
        chunks.push({
          id: `sec-${chunkStart}`,
          startLine: chunkStart,
          endLine: lines.length,
          markdown: tail,
        })
      }
    }
    if (chunks.length > 1) {
      return chunks
    }
  }

  return sections
}

export function replaceMarkdownSection(
  originalMarkdown: string,
  section: MarkdownSection,
  replacement: string,
): string {
  const eol = originalMarkdown.includes('\r\n') ? '\r\n' : '\n'
  const lines = originalMarkdown.replace(/\r\n/g, '\n').split('\n')
  const replacementLines = replacement.replace(/\r\n/g, '\n').trimEnd().split('\n')
  const next = [
    ...lines.slice(0, section.startLine),
    ...replacementLines,
    ...lines.slice(section.endLine),
  ]
  return next.join(eol)
}

const QUICK_REFINES = [
  { label: '更精炼', instruction: '压缩篇幅 35%，保留核心事实与关键数字，删除冗余表述。' },
  { label: '偏正式汇报', instruction: '改写为面向管理层的正式书面汇报口吻，结论清晰、逻辑紧凑。' },
  { label: '扩写细节', instruction: '补充清晰的背景、执行要点与落地影响，但不编造未提及的指标。' },
  { label: '转为要点', instruction: '将正文梳理为结构化的短句项目符号列表，便于快速扫读。' },
  { label: '对外脱敏', instruction: '隐去内网 IP、本机路径、库名表名与内部代号，改写为可对外发送的表述。' },
]

export interface MarkdownRefineViewProps {
  markdown: string
  documentTitle: string
  session: Session | null
  writable: boolean
  onApplyChange: (nextMarkdown: string) => void
  /** Optional auto-save callback invoked right after applying a refined paragraph */
  onSaveNow?: (nextMarkdown: string) => Promise<boolean>
}

export function MarkdownRefineView({
  markdown,
  documentTitle,
  session,
  writable,
  onApplyChange,
  onSaveNow,
}: MarkdownRefineViewProps) {
  const sections = useMemo(() => splitMarkdownSections(markdown), [markdown])
  const outlineItems = useMemo(
    () => sections.filter((s) => Boolean(s.heading)),
    [sections],
  )
  const [showOutline, setShowOutline] = useState(outlineItems.length >= 2)
  const [activeSectionId, setActiveSectionId] = useState<string | null>(null)
  const [instruction, setInstruction] = useState('')
  const [refining, setRefining] = useState(false)
  const [lastUndoMarkdown, setLastUndoMarkdown] = useState<string | null>(null)
  const [savingApply, setSavingApply] = useState(false)
  const [providers, setProviders] = useState<AiProvider[]>([])
  const [selectedModelKey, setSelectedModelKey] = useState<string>('')
  const [candidate, setCandidate] = useState<{
    sectionId: string
    original: string
    rewritten: string
    mode: string
    model?: string
    assembledPrompt: string
    message?: string
  } | null>(null)

  useEffect(() => {
    let active = true
    if (typeof api.getAiProviders !== 'function') return
    api.getAiProviders()
      .then((cfg) => {
        if (!active) return
        setProviders((cfg.providers || []).filter((p) => p.enabled !== false))
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])

  const modelOptions = useMemo(() => {
    const items: Array<{ value: string; label: string }> = [{ value: '', label: '默认 AI 模型' }]
    for (const p of providers) {
      const models = p.models && p.models.length > 0 ? p.models : []
      if (models.length === 0) {
        items.push({ value: `${p.id}::`, label: `${p.name}（默认）` })
      } else {
        for (const m of models.slice(0, 20)) {
          items.push({ value: `${p.id}::${m}`, label: `${p.name} · ${m}` })
        }
      }
    }
    return items
  }, [providers])

  const runRefine = async (section: MarkdownSection, customInstr?: string) => {
    const reqInstruction = (customInstr ?? instruction).trim()
    if (!reqInstruction) {
      notifications.show({ color: 'yellow', message: '请选择快捷指令或输入改写要求' })
      return
    }
    const idx = sections.findIndex((s) => s.id === section.id)
    const before = idx > 0 ? sections[idx - 1].markdown : ''
    const after = idx >= 0 && idx < sections.length - 1 ? sections[idx + 1].markdown : ''
    const [providerId, modelId] = selectedModelKey ? selectedModelKey.split('::') : ['', '']

    setRefining(true)
    try {
      const resp = await api.refineOfficeSelection({
        document_title: documentTitle,
        selected_text: section.markdown,
        context_before: before,
        context_after: after,
        instruction: reqInstruction,
        ...(providerId ? { provider_id: providerId } : {}),
        ...(modelId ? { model: modelId } : {}),
      })
      setCandidate({
        sectionId: section.id,
        original: section.markdown.trim(),
        rewritten: resp.rewritten_text.trim(),
        mode: resp.mode,
        model: resp.model,
        assembledPrompt: resp.assembled_prompt,
        message: resp.message,
      })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '局部改写失败',
      })
    } finally {
      setRefining(false)
    }
  }

  const sendSectionToChat = (section: MarkdownSection, customInstr?: string) => {
    const reqInstruction = (customInstr ?? instruction).trim() || '请帮我润色并优化这段文字'
    const text = `请帮我局部改写文档《${documentTitle}》中的以下段落（改写要求：${reqInstruction}）：\n\n${section.markdown}`
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: session?.agent_id,
          sessionId: session?.id,
          text,
        },
      }),
    )
    notifications.show({ color: 'teal', message: '已发送到当前对话输入框' })
  }

  const applyCandidate = async (section: MarkdownSection) => {
    if (!candidate) return
    const previousMarkdown = markdown
    const nextMarkdown = replaceMarkdownSection(markdown, section, candidate.rewritten)
    setLastUndoMarkdown(previousMarkdown)
    onApplyChange(nextMarkdown)
    setCandidate(null)
    setActiveSectionId(null)
    setInstruction('')
    if (onSaveNow) {
      setSavingApply(true)
      try {
        const saved = await onSaveNow(nextMarkdown)
        notifications.show({
          color: 'teal',
          message: saved ? '已应用段落修改并自动保存' : '已应用段落修改，请手动保存',
        })
      } finally {
        setSavingApply(false)
      }
    } else {
      notifications.show({ color: 'teal', message: '已应用段落修改' })
    }
  }

  const handleUndoLastApply = async () => {
    if (lastUndoMarkdown === null) return
    const restore = lastUndoMarkdown
    setLastUndoMarkdown(null)
    onApplyChange(restore)
    if (onSaveNow) {
      await onSaveNow(restore)
    }
    notifications.show({ color: 'teal', message: '已撤销上次段落替换' })
  }

  const diffResult = useMemo(() => {
    if (!candidate || candidate.mode !== 'llm') return null
    return diffDocxParagraphs(
      candidate.original.split('\n'),
      candidate.rewritten.split('\n'),
    )
  }, [candidate])

  return (
    <div className="md-refine-root">
      {showOutline && outlineItems.length > 0 && (
        <aside className="md-refine-outline" aria-label="文档目录">
          <div className="md-refine-outline-title">目录大纲</div>
          {outlineItems.map((sec) => (
            <button
              key={sec.id}
              type="button"
              className={`md-refine-outline-btn${activeSectionId === sec.id ? ' is-active' : ''}`}
              style={{ paddingLeft: 8 + Math.max(0, (sec.level || 1) - 1) * 12 }}
              onClick={() => {
                setActiveSectionId(sec.id)
                document.getElementById(`md-refine-${sec.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
              }}
              title={sec.heading}
            >
              {sec.heading}
            </button>
          ))}
        </aside>
      )}

      <div className="md-refine-scroll">
        {(outlineItems.length > 0 || lastUndoMarkdown !== null) && (
          <Group justify="space-between" mb={8} style={{ maxWidth: 820, marginInline: 'auto' }}>
            <Group gap={8}>
              {outlineItems.length > 0 && (
                <Tooltip label={showOutline ? '收起目录' : '展开目录'}>
                  <ActionIcon
                    variant={showOutline ? 'light' : 'subtle'}
                    size="sm"
                    aria-label="切换目录大纲"
                    onClick={() => setShowOutline((v) => !v)}
                  >
                    <IconLayoutSidebar size={15} />
                  </ActionIcon>
                </Tooltip>
              )}
              <Text size="xs" c="dimmed">
                悬停任意段落可局部精修并对比替换
              </Text>
            </Group>
            {lastUndoMarkdown !== null && (
              <Button
                size="compact-xs"
                variant="light"
                color="gray"
                leftSection={<IconArrowBackUp size={13} />}
                onClick={() => void handleUndoLastApply()}
              >
                撤销上次替换
              </Button>
            )}
          </Group>
        )}

        <div className="md-refine-paper">
          {sections.map((section) => {
            const isEditing = activeSectionId === section.id
            const sectionCandidate = candidate?.sectionId === section.id ? candidate : null
            return (
              <div
                key={section.id}
                id={`md-refine-${section.id}`}
                className={`md-refine-section${isEditing ? ' is-editing' : ''}`}
              >
                <div className="md-refine-section-trigger">
                  <Button
                    size="compact-xs"
                    variant={isEditing ? 'filled' : 'light'}
                    color="indigo"
                    leftSection={<IconSparkles size={12} />}
                    onClick={() => {
                      if (isEditing) {
                        setActiveSectionId(null)
                        setCandidate(null)
                      } else {
                        setActiveSectionId(section.id)
                        setCandidate(null)
                      }
                    }}
                  >
                    {isEditing ? '收起' : '精修这段'}
                  </Button>
                </div>

                <MarkdownContent value={section.markdown} session={session} />

                {isEditing && (
                  <div className="md-refine-panel" aria-label="段落局部精修">
                    <Group justify="space-between" gap="xs" wrap="wrap">
                      <div className="md-refine-pills">
                        {QUICK_REFINES.map((preset) => (
                          <Button
                            key={preset.label}
                            size="compact-xs"
                            variant="light"
                            color="gray"
                            disabled={refining}
                            onClick={() => {
                              setInstruction(preset.instruction)
                              void runRefine(section, preset.instruction)
                            }}
                          >
                            {preset.label}
                          </Button>
                        ))}
                      </div>
                      {modelOptions.length > 1 && (
                        <Select
                          size="xs"
                          w={190}
                          aria-label="精修模型"
                          value={selectedModelKey}
                          onChange={(val) => setSelectedModelKey(val || '')}
                          data={modelOptions}
                          allowDeselect={false}
                        />
                      )}
                    </Group>

                    <Group gap={6} wrap="nowrap">
                      <TextInput
                        size="xs"
                        style={{ flex: 1 }}
                        placeholder="输入改写要求，如：精简为三句话、突出交付收益…"
                        value={instruction}
                        onChange={(e) => setInstruction(e.currentTarget.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' && !e.shiftKey) {
                            e.preventDefault()
                            void runRefine(section)
                          }
                        }}
                      />
                      <Button
                        size="xs"
                        color="indigo"
                        loading={refining}
                        leftSection={<IconSparkles size={13} />}
                        onClick={() => void runRefine(section)}
                      >
                        改写
                      </Button>
                      <Button
                        size="xs"
                        variant="light"
                        color="gray"
                        leftSection={<IconMessageForward size={13} />}
                        onClick={() => sendSectionToChat(section)}
                      >
                        发给对话
                      </Button>
                    </Group>

                    {sectionCandidate && (
                      <>
                        {sectionCandidate.mode === 'prompt_only' ? (
                          <Group justify="space-between" gap="xs">
                            <Text size="xs" c="dimmed">
                              {sectionCandidate.message || '未配置 AI 供应商模型，可直接发送到当前对话改写。'}
                            </Text>
                            <Button
                              size="compact-xs"
                              variant="light"
                              color="indigo"
                              leftSection={<IconMessageForward size={12} />}
                              onClick={() => {
                                window.dispatchEvent(
                                  new CustomEvent('astrorder:quote-to-composer', {
                                    detail: {
                                      agentId: session?.agent_id,
                                      sessionId: session?.id,
                                      text: sectionCandidate.assembledPrompt,
                                    },
                                  }),
                                )
                                notifications.show({ color: 'teal', message: '已发送改写指令到当前对话' })
                              }}
                            >
                              发送完整提示词
                            </Button>
                          </Group>
                        ) : (
                          <>
                            <Group justify="space-between" gap="xs">
                              <Group gap={6}>
                                <Badge size="xs" variant="light" color="teal">
                                  改写预览
                                </Badge>
                                {sectionCandidate.model && (
                                  <Text size="xs" c="dimmed">{sectionCandidate.model}</Text>
                                )}
                              </Group>
                              <Group gap={6}>
                                <Button
                                  size="compact-xs"
                                  variant="subtle"
                                  color="gray"
                                  leftSection={<IconX size={12} />}
                                  onClick={() => setCandidate(null)}
                                >
                                  放弃
                                </Button>
                                <Button
                                  size="compact-xs"
                                  color="teal"
                                  disabled={!writable}
                                  loading={savingApply}
                                  leftSection={<IconCheck size={12} />}
                                  onClick={() => void applyCandidate(section)}
                                >
                                  应用到正文
                                </Button>
                              </Group>
                            </Group>

                            {diffResult && (
                              <div className="md-refine-diff-box" aria-label="改写对比">
                                {diffResult.entries.map((entry, idx) => (
                                  <div key={idx} className={`md-refine-diff-line is-${entry.kind}`}>
                                    {entry.kind === 'removed' ? '- ' : entry.kind === 'added' ? '+ ' : '  '}
                                    {entry.text || ' '}
                                  </div>
                                ))}
                              </div>
                            )}
                          </>
                        )}
                      </>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
