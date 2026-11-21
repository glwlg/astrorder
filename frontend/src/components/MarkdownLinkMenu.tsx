import { Menu } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCopy, IconDownload, IconExternalLink, IconFileText } from '@tabler/icons-react'
import { request } from '../api/client'
import type { Session } from '../domain/types'
import { downloadFile, fileDesktop } from '../features/sidecar/fileTransfer'

export type MarkdownLinkMenuTarget = {
  x: number
  y: number
  kind: 'url' | 'file'
  value: string
}

async function openFile(path: string, session: Session | null | undefined) {
  if (!fileDesktop()) throw new Error('请在星序桌面端打开文件')
  if (!session) throw new Error('无法确定文件所在的会话')

  let localPath = path.replace(/:\d+(?::\d+)?$/, '')
  if (!/^(?:[a-zA-Z]:[/\\]|\/|\\\\)/.test(localPath)) throw new Error('无法确定文件的绝对路径')
  if (session.connection_id && session.connection_id !== 'local') {
    const staged = await request<{ local_paths: string[] }>('/files/stage', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        paths: [localPath],
        session_id: session.id,
        connection_id: session.connection_id,
        copy_to_clipboard: false,
      }),
    })
    if (!staged.local_paths?.[0]) throw new Error('未获取到暂存文件路径')
    localPath = staged.local_paths[0]
  }

  await request('/system/open-file', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: localPath, action: 'open' }),
  })
}

function rawDownloadTarget(path: string, session: Session | null | undefined): { url: string; filename: string } {
  const cleanPath = path.replace(/:\d+(?::\d+)?$/, '')
  const filename = cleanPath.split(/[/\\]/).filter(Boolean).pop() || 'download'
  const query = new URLSearchParams({ path: cleanPath, download: '1' })
  if (session?.id) query.set('session_id', session.id)
  if (session?.connection_id) query.set('connection_id', session.connection_id)
  return { url: `/api/v1/files/raw?${query.toString()}`, filename }
}

export function MarkdownLinkMenu({
  target,
  session,
  onClose,
}: {
  target: MarkdownLinkMenuTarget | null
  session?: Session | null
  onClose: () => void
}) {
  const showError = (error: unknown) => notifications.show({
    color: 'red',
    title: '打开失败',
    message: error instanceof Error ? error.message : '请稍后重试',
  })

  const handleFile = () => {
    if (!target) return
    const id = notifications.show({ loading: true, title: '正在打开文件', message: target.value, autoClose: false })
    void openFile(target.value, session)
      .then(() => notifications.hide(id))
      .catch((error: unknown) => notifications.update({
        id,
        loading: false,
        color: 'red',
        title: '打开文件失败',
        message: error instanceof Error ? error.message : '请稍后重试',
        autoClose: 5000,
      }))
  }

  return (
    <Menu opened={target !== null} onChange={(opened) => { if (!opened) onClose() }} position="bottom-start" withinPortal shadow="md">
      <Menu.Target>
        <span style={{ position: 'fixed', left: target?.x ?? 0, top: target?.y ?? 0, width: 1, height: 1, visibility: 'hidden', pointerEvents: 'none' }} />
      </Menu.Target>
      {target && <Menu.Dropdown>
        {target.kind === 'url' ? (
          <Menu.Item
            component="a"
            href={target.value}
            target="_blank"
            rel="noopener noreferrer"
            leftSection={<IconExternalLink size={14} />}
            onClick={(event) => {
              const desktop = fileDesktop()
              if (!desktop?.openExternal) return
              event.preventDefault()
              void desktop.openExternal(target.value).catch(showError)
            }}
          >
            在默认浏览器中打开
          </Menu.Item>
        ) : (
          <>
            <Menu.Item leftSection={<IconFileText size={14} />} onClick={handleFile}>
              用系统默认应用打开
            </Menu.Item>
            <Menu.Item
              leftSection={<IconDownload size={14} />}
              onClick={() => {
                const { url, filename } = rawDownloadTarget(target.value, session)
                void downloadFile(url, filename)
              }}
            >
              下载到本地
            </Menu.Item>
          </>
        )}
        <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => {
          if (!navigator.clipboard?.writeText) {
            showError(new Error('当前环境无法访问剪贴板'))
            return
          }
          void navigator.clipboard.writeText(target.value)
            .then(() => notifications.show({ color: 'teal', message: target.kind === 'url' ? '链接已复制' : '路径已复制' }))
            .catch(showError)
        }}>
          {target.kind === 'url' ? '复制链接' : '复制路径'}
        </Menu.Item>
      </Menu.Dropdown>}
    </Menu>
  )
}
