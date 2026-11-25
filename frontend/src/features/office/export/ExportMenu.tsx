import { useMemo, useState } from 'react'
import { ActionIcon, Button, Menu, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconClipboardText,
  IconDeviceFloppy,
  IconDownload,
  IconFileCode2,
  IconFileSpreadsheet,
  IconFileText,
  IconFileTypeDocx,
  IconFileTypePdf,
  IconLoader2,
  IconPresentation,
} from '@tabler/icons-react'
import { api } from '../../../api/client'
import { exportMarkdownTablesToXlsx, exportMarkdownToDocx, exportMarkdownToPptx, hasMarkdownTable } from './exportService'
import { downloadStandaloneHtml, printMarkdownToPdf } from './printPdf'
import { copyMarkdownAsRichText } from './richClipboard'

export interface ExportMenuProps {
  markdown: string
  title?: string
  subtitle?: string
  fileName?: string
  /** Optional target workspace / session so users can save exports directly into their office workspace */
  workspace?: string | null
  sessionId?: string | null
  connectionId?: string | null
  /** 'icon' for compact action bars (Transcript), 'button' for toolbars (Monaco / Digest) */
  variant?: 'icon' | 'button'
  buttonLabel?: string
  disabled?: boolean
}

function downloadMarkdownFile(markdown: string, fileName?: string, title?: string): void {
  const base = (fileName || title || '文档导出').replace(/[<>:"/\\|?*]+/g, '_').replace(/\.md$/i, '').trim() || '文档导出'
  const blob = new Blob([markdown], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `${base}.md`
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function ExportMenu({
  markdown,
  title,
  subtitle,
  fileName,
  workspace,
  sessionId,
  connectionId,
  variant = 'button',
  buttonLabel = '导出',
  disabled = false,
}: ExportMenuProps) {
  const [busyAction, setBusyAction] = useState<string | null>(null)
  const [copiedRich, setCopiedRich] = useState(false)

  const hasTables = useMemo(() => hasMarkdownTable(markdown), [markdown])
  const canSaveToWorkspace = Boolean((workspace && workspace.trim()) || (sessionId && sessionId.trim()))
  const isEmpty = !markdown.trim()
  const isDisabled = disabled || isEmpty || busyAction !== null

  const runAction = async (key: string, fn: () => Promise<void>, successMessage?: string) => {
    setBusyAction(key)
    try {
      await fn()
      if (successMessage) {
        notifications.show({ color: 'teal', message: successMessage })
      }
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '导出操作失败',
      })
    } finally {
      setBusyAction(null)
    }
  }

  const saveToWorkspace = (format: 'md' | 'docx' | 'pptx', formatLabel: string) => {
    void runAction(`save-${format}`, async () => {
      const res = await api.saveOfficeExport({
        format,
        markdown,
        title,
        subtitle,
        file_name: fileName || title,
        workspace: workspace || undefined,
        session_id: sessionId || undefined,
        connection_id: connectionId || undefined,
      })
      notifications.show({
        color: 'teal',
        message: `已保存 ${formatLabel} 到工作区：${res.name}`,
      })
    })
  }

  return (
    <Menu shadow="md" width={216} position="bottom-end" withinPortal>
      <Menu.Target>
        {variant === 'icon' ? (
          <Tooltip label={busyAction ? '正在导出…' : '导出'} position="top" withArrow>
            <ActionIcon
              variant="subtle"
              size="xs"
              color="gray"
              disabled={isDisabled}
              aria-label="导出文档"
              className="message-action-btn"
            >
              {busyAction ? (
                <IconLoader2 size={13} className="astrorder-spin" />
              ) : copiedRich ? (
                <IconCheck size={13} color="var(--astr-teal, #12b886)" />
              ) : (
                <IconDownload size={13} />
              )}
            </ActionIcon>
          </Tooltip>
        ) : (
          <Button
            size="compact-xs"
            variant="light"
            color="indigo"
            disabled={isDisabled}
            loading={busyAction !== null}
            leftSection={<IconDownload size={13} />}
          >
            {buttonLabel}
          </Button>
        )}
      </Menu.Target>

      <Menu.Dropdown>
        <Menu.Item
          leftSection={<IconClipboardText size={15} />}
          onClick={() =>
            void runAction(
              'rich',
              async () => {
                await copyMarkdownAsRichText(markdown, { title, subtitle })
                setCopiedRich(true)
                window.setTimeout(() => setCopiedRich(false), 1800)
              },
              '已复制富文本',
            )
          }
        >
          复制富文本
        </Menu.Item>
        <Menu.Item
          leftSection={<IconFileTypeDocx size={15} color="#2563eb" />}
          onClick={() =>
            void runAction(
              'docx',
              () => exportMarkdownToDocx({ markdown, title, subtitle, fileName }),
              '已导出 Word (.docx)',
            )
          }
        >
          导出 Word (.docx)
        </Menu.Item>
        <Menu.Item
          leftSection={<IconPresentation size={15} color="#ea580c" />}
          onClick={() =>
            void runAction(
              'pptx',
              () => exportMarkdownToPptx({ markdown, title, subtitle, fileName }),
              '已导出 PPT (.pptx)',
            )
          }
        >
          导出 PPT (.pptx)
        </Menu.Item>
        <Menu.Item
          leftSection={<IconFileTypePdf size={15} color="#dc2626" />}
          onClick={() =>
            void runAction('pdf', () => printMarkdownToPdf({ markdown, title, subtitle }))
          }
        >
          导出 PDF
        </Menu.Item>
        <Menu.Item
          leftSection={<IconFileText size={15} color="#475569" />}
          onClick={() =>
            void runAction(
              'md',
              async () => {
                downloadMarkdownFile(markdown, fileName, title)
              },
              '已导出 Markdown (.md)',
            )
          }
        >
          导出 Markdown (.md)
        </Menu.Item>
        <Menu.Item
          leftSection={<IconFileCode2 size={15} color="#0f766e" />}
          onClick={() =>
            void runAction(
              'html',
              () => downloadStandaloneHtml({ markdown, title, subtitle, fileName }),
              '已导出 HTML',
            )
          }
        >
          导出 HTML
        </Menu.Item>
        {hasTables && (
          <>
            <Menu.Divider />
            <Menu.Item
              leftSection={<IconFileSpreadsheet size={15} color="#15803d" />}
              onClick={() =>
                void runAction(
                  'xlsx',
                  () => exportMarkdownTablesToXlsx({ markdown, fileName: fileName || title }),
                  '已导出 Excel (.xlsx)',
                )
              }
            >
              导出 Excel (.xlsx)
            </Menu.Item>
          </>
        )}
        {canSaveToWorkspace && (
          <>
            <Menu.Divider />
            <Menu.Label>保存到当前工作区</Menu.Label>
            <Menu.Item
              leftSection={<IconDeviceFloppy size={15} color="#475569" />}
              onClick={() => saveToWorkspace('md', 'Markdown')}
            >
              保存为 Markdown (.md)
            </Menu.Item>
            <Menu.Item
              leftSection={<IconFileTypeDocx size={15} color="#2563eb" />}
              onClick={() => saveToWorkspace('docx', 'Word')}
            >
              保存为 Word (.docx)
            </Menu.Item>
            <Menu.Item
              leftSection={<IconPresentation size={15} color="#ea580c" />}
              onClick={() => saveToWorkspace('pptx', 'PPT')}
            >
              保存为 PPT (.pptx)
            </Menu.Item>
          </>
        )}
      </Menu.Dropdown>
    </Menu>
  )
}
