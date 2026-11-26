import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Badge, Button, Checkbox, Group, Loader, Modal, Stack, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBook2,
  IconColumns,
  IconFileSpreadsheet,
  IconFileText,
  IconFileTypeDocx,
  IconFileTypePdf,
  IconLoader2,
  IconMovie,
  IconMusic,
  IconPlus,
  IconPresentation,
  IconSearch,
  IconShieldCheck,
  IconSparkles,
  IconSubtitles,
  IconTable,
  IconUpload,
  IconX,
} from '@tabler/icons-react'
import { api, type RecentOfficeDoc } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import './officeStudio.css'

export interface PinnedOfficeSource {
  path: string
  name: string
  ext: string
}

const SOURCE_ACTIONS = [
  { id: 'summary', label: '综合摘要', icon: IconSparkles, desc: '提炼核心结论并标注出处' },
  { id: 'compare', label: '交叉对比', icon: IconColumns, desc: '对比多份文档异同与冲突' },
  { id: 'table', label: '汇总成表', icon: IconTable, desc: '整理结构化对比表格' },
  { id: 'ppt_outline', label: '生成 PPT 大纲', icon: IconPresentation, desc: '逐页生成演示文稿大纲' },
  { id: 'review', label: '合规审查', icon: IconShieldCheck, desc: '排查合同/方案风险点' },
] as const

function renderSourceIcon(name: string) {
  const lower = name.toLowerCase()
  if (lower.endsWith('.docx') || lower.endsWith('.doc') || lower.endsWith('.wps')) {
    return <IconFileTypeDocx size={13} color="#2563eb" />
  }
  if (lower.endsWith('.xlsx') || lower.endsWith('.xls') || lower.endsWith('.csv')) {
    return <IconFileSpreadsheet size={13} color="#15803d" />
  }
  if (lower.endsWith('.pptx') || lower.endsWith('.ppt')) {
    return <IconPresentation size={13} color="#ea580c" />
  }
  if (lower.endsWith('.pdf')) {
    return <IconFileTypePdf size={13} color="#dc2626" />
  }
  if (/\.(mp4|m4v|webm|mov|mkv|ogv)$/i.test(lower)) {
    return <IconMovie size={13} color="#4f46e5" />
  }
  if (/\.(wav|mp3|m4a|ogg|flac|aac|opus)$/i.test(lower)) {
    return <IconMusic size={13} color="#0d9488" />
  }
  if (/\.(srt|vtt)$/i.test(lower)) {
    return <IconSubtitles size={13} color="#d97706" />
  }
  return <IconFileText size={13} color="#52525b" />
}

export interface OfficeSourcesBarProps {
  session: Session
  pinnedSources: PinnedOfficeSource[]
  onChangeSources: (next: PinnedOfficeSource[]) => void
  onSendPromptToChat: (prompt: string) => void
  onOpenTemplateModal: () => void
}

export function OfficeSourcesBar({
  session,
  pinnedSources,
  onChangeSources,
  onSendPromptToChat,
  onOpenTemplateModal,
}: OfficeSourcesBarProps) {
  const [pickerOpen, setPickerOpen] = useState(false)
  const [workspaceDocs, setWorkspaceDocs] = useState<RecentOfficeDoc[]>([])
  const [loadingDocs, setLoadingDocs] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [synthesizingAction, setSynthesizingAction] = useState<string | null>(null)
  const [uploading, setUploading] = useState(false)
  const uploadRef = useRef<HTMLInputElement>(null)

  const loadWorkspaceDocs = useCallback(async () => {
    setLoadingDocs(true)
    try {
      const resp = await api.listRecentOfficeDocs({
        workspace: session.workspace || undefined,
        limit: 30,
      })
      setWorkspaceDocs(resp.items || [])
    } catch {
      // Fallback to searchFiles when remote or workspace not locally indexed
      try {
        const res = await api.searchFiles({
          q: '',
          sessionId: session.id,
          connectionId: session.connection_id || undefined,
          limit: 30,
        })
        setWorkspaceDocs(
          (res.items || [])
            .filter((it) => /\.(docx?|xlsx?|pptx?|pdf|md|markdown|csv|txt|wps|srt|vtt|mp4|m4v|webm|mov|mkv|wav|mp3|m4a|ogg|flac)$/i.test(it.name))
            .map((it) => ({
              path: it.path,
              name: it.name,
              ext: it.name.split('.').pop()?.toLowerCase() || '',
              size: it.size,
              mod_time: '',
              workspace: session.workspace || '',
            })),
        )
      } catch {
        setWorkspaceDocs([])
      }
    } finally {
      setLoadingDocs(false)
    }
  }, [session.connection_id, session.id, session.workspace])

  useEffect(() => {
    if (pickerOpen) {
      void loadWorkspaceDocs()
    }
  }, [loadWorkspaceDocs, pickerOpen])

  const filteredDocs = useMemo(() => {
    const q = searchQuery.trim().toLowerCase()
    if (!q) return workspaceDocs
    return workspaceDocs.filter(
      (d) => d.name.toLowerCase().includes(q) || d.path.toLowerCase().includes(q),
    )
  }, [searchQuery, workspaceDocs])

  const pinnedSet = useMemo(() => new Set(pinnedSources.map((s) => s.path)), [pinnedSources])

  const toggleSource = (doc: { path: string; name: string; ext?: string }) => {
    if (pinnedSet.has(doc.path)) {
      onChangeSources(pinnedSources.filter((s) => s.path !== doc.path))
    } else {
      if (pinnedSources.length >= 8) {
        notifications.show({ color: 'yellow', message: '单次最多勾选 8 份参考资料' })
        return
      }
      onChangeSources([
        ...pinnedSources,
        { path: doc.path, name: doc.name, ext: doc.ext || doc.name.split('.').pop()?.toLowerCase() || '' },
      ])
    }
  }

  const handleUploadSource = async (files: FileList | null) => {
    if (!files || files.length === 0) return
    setUploading(true)
    try {
      const added: PinnedOfficeSource[] = []
      for (const file of Array.from(files)) {
        const res = await api.uploadFile({
          file,
          parent: session.workspace || undefined,
          session_id: session.id,
          connection_id: session.connection_id || null,
        })
        added.push({
          path: res.path,
          name: res.name,
          ext: res.name.split('.').pop()?.toLowerCase() || '',
        })
      }
      const merged = [...pinnedSources]
      for (const item of added) {
        if (!merged.some((m) => m.path === item.path) && merged.length < 8) {
          merged.push(item)
        }
      }
      onChangeSources(merged)
      notifications.show({ color: 'teal', message: `已上传并加入 ${added.length} 份资料` })
      if (pickerOpen) void loadWorkspaceDocs()
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '上传资料失败' })
    } finally {
      setUploading(false)
    }
  }

  const runSynthesizeAction = async (actionId: string) => {
    if (pinnedSources.length === 0) return
    setSynthesizingAction(actionId)
    try {
      const resp = await api.synthesizeOfficeSources({
        paths: pinnedSources.map((s) => s.path),
        action: actionId,
        session_id: session.id,
        connection_id: session.connection_id || undefined,
        mode: 'llm',
      })
      if (resp.mode === 'prompt_only') {
        onSendPromptToChat(resp.assembled_prompt)
        notifications.show({
          color: 'teal',
          message: '已抽取多文档资料并填入对话，按发送即可开始综合分析',
        })
        return
      }

      // Save generated deliverable directly into workspace if available, then open in Rich Document Studio!
      let savedPath: string | undefined
      if (session.workspace) {
        try {
          const saved = await api.saveOfficeExport({
            format: 'md',
            markdown: resp.markdown,
            title: resp.title,
            file_name: resp.title,
            workspace: session.workspace,
            session_id: session.id,
            connection_id: session.connection_id || undefined,
          })
          savedPath = saved.path
        } catch {
          // fallback to inline editable canvas
        }
      }

      if (savedPath) {
        const artifact = resolveArtifactFromPath(savedPath, session, { name: `${resp.title}.md` })
        useSidecarStore.getState().openArtifact(
          { ...artifact, metadata: { ...artifact.metadata, preferRich: true } },
          'monaco-viewer',
        )
      } else {
        useSidecarStore.getState().openArtifact(
          {
            id: `synth:${Date.now()}`,
            sessionId: session.id,
            agentId: session.agent_id,
            connectionId: session.connection_id || undefined,
            name: `${resp.title}.md`,
            kind: 'workspace_file',
            readUrl: '',
            mediaType: 'text/markdown',
            writable: false,
            metadata: {
              inlineContent: resp.markdown,
              inlineEditable: true,
              preferRich: true,
            },
          },
          'monaco-viewer',
        )
      }
      notifications.show({
        color: 'teal',
        message: savedPath
          ? `已完成多文档「${resp.title}」并保存至工作区，已在写作台打开`
          : `已完成多文档「${resp.title}」，已在写作台打开`,
      })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '多文档协同处理失败' })
    } finally {
      setSynthesizingAction(null)
    }
  }

  return (
    <div className="office-sources-strip" aria-label="办公资料包与场景工具条">
      <input
        ref={uploadRef}
        type="file"
        multiple
        accept=".docx,.doc,.xlsx,.xls,.pptx,.ppt,.pdf,.md,.markdown,.csv,.txt,.wps,.srt,.vtt,.mp4,.m4v,.webm,.mov,.mkv,.wav,.mp3,.m4a,.ogg,.flac"
        style={{ display: 'none' }}
        onChange={(e) => {
          void handleUploadSource(e.currentTarget.files)
          e.currentTarget.value = ''
        }}
      />

      <div className="office-sources-row">
        <button
          type="button"
          className="office-tool-chip is-primary"
          onClick={onOpenTemplateModal}
        >
          <IconSparkles size={12} />
          <span>场景模板起草</span>
        </button>

        <button
          type="button"
          className={`office-tool-chip${pinnedSources.length > 0 ? ' is-active' : ''}`}
          onClick={() => setPickerOpen(true)}
        >
          <IconBook2 size={12} />
          <span>
            {pinnedSources.length > 0 ? `资料包 (${pinnedSources.length})` : '勾选参考资料'}
          </span>
          <IconPlus size={11} />
        </button>

        <Tooltip label="直接上传文档、字幕或音视频素材到工作空间并加入资料包">
          <button
            type="button"
            className="office-tool-chip"
            disabled={uploading}
            onClick={() => uploadRef.current?.click()}
          >
            {uploading ? <IconLoader2 size={12} className="astrorder-spin" /> : <IconUpload size={12} />}
            <span>传资料</span>
          </button>
        </Tooltip>

        {pinnedSources.map((src) => (
          <span key={src.path} className="office-source-pill" title={src.path}>
            {renderSourceIcon(src.name)}
            <span className="office-source-pill-name">{src.name}</span>
            <button
              type="button"
              className="office-source-pill-remove"
              aria-label={`移除参考资料 ${src.name}`}
              onClick={() => toggleSource(src)}
            >
              <IconX size={11} />
            </button>
          </span>
        ))}
      </div>

      {pinnedSources.length > 0 && (
        <div className="office-sources-actions" role="group" aria-label="多文档协同操作">
          <span className="office-sources-hint">基于已选 {pinnedSources.length} 份资料：</span>
          {SOURCE_ACTIONS.map((act) => {
            const Icon = act.icon
            const busy = synthesizingAction === act.id
            return (
              <Tooltip key={act.id} label={act.desc} withArrow>
                <button
                  type="button"
                  className="office-synth-btn"
                  disabled={synthesizingAction !== null}
                  onClick={() => void runSynthesizeAction(act.id)}
                >
                  {busy ? <IconLoader2 size={12} className="astrorder-spin" /> : <Icon size={12} />}
                  <span>{act.label}</span>
                </button>
              </Tooltip>
            )
          })}
          <button
            type="button"
            className="office-synth-clear"
            onClick={() => onChangeSources([])}
          >
            清空资料
          </button>
        </div>
      )}

      <Modal
        opened={pickerOpen}
        onClose={() => setPickerOpen(false)}
        title={
          <Group gap={8}>
            <IconBook2 size={17} />
            <Text size="sm" fw={650}>选择参考资料（多文档 AI 协同）</Text>
            <Badge size="xs" variant="light" color="gray">{pinnedSources.length} / 8</Badge>
          </Group>
        }
        size="md"
        withinPortal
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            勾选多份 Word、Excel、PPT、PDF 或 Markdown 文档作为资料包。后续提问将自动附带这些资料的精准摘录与出处编号，或直接一键对比、汇总成表与生成 PPT 大纲。
          </Text>
          <Group gap="xs">
            <TextInput
              size="xs"
              style={{ flex: 1 }}
              leftSection={<IconSearch size={13} />}
              placeholder="搜索当前工作空间文档…"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.currentTarget.value)}
            />
            <Button
              size="xs"
              variant="default"
              loading={uploading}
              leftSection={<IconUpload size={13} />}
              onClick={() => uploadRef.current?.click()}
            >
              上传新资料
            </Button>
          </Group>

          <div className="office-source-picker-list">
            {loadingDocs ? (
              <Group justify="center" py="lg">
                <Loader size="xs" />
                <Text size="xs" c="dimmed">正在读取工作空间文档…</Text>
              </Group>
            ) : filteredDocs.length === 0 ? (
              <Text size="xs" c="dimmed" ta="center" py="lg">
                当前工作空间暂无可选办公文档，可点击右侧「上传新资料」导入。
              </Text>
            ) : (
              filteredDocs.map((doc) => {
                const checked = pinnedSet.has(doc.path)
                return (
                  <label key={doc.path} className={`office-source-picker-row${checked ? ' is-checked' : ''}`}>
                    <Checkbox
                      size="xs"
                      checked={checked}
                      onChange={() => toggleSource(doc)}
                    />
                    {renderSourceIcon(doc.name)}
                    <div className="office-source-picker-copy">
                      <span className="office-source-picker-name">{doc.name}</span>
                      <span className="office-source-picker-path">{doc.path}</span>
                    </div>
                  </label>
                )
              })
            )}
          </div>

          <Group justify="flex-end" gap="xs">
            {pinnedSources.length > 0 && (
              <Button size="xs" variant="subtle" color="gray" onClick={() => onChangeSources([])}>
                清空已选
              </Button>
            )}
            <Button size="xs" color="dark" onClick={() => setPickerOpen(false)}>
              完成选择 ({pinnedSources.length})
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
