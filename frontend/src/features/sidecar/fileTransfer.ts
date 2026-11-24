import { notifications } from '@mantine/notifications'
import { authenticatedApiFetch } from '../../api/client'

type FileDesktop = {
  setClipboardFiles?: (paths: string[]) => Promise<boolean>
  saveDownload?: (url: string, filename: string) => Promise<boolean>
  openExternal?: (url: string) => Promise<void>
}
export function fileDesktop(): FileDesktop | undefined {
  return (window as unknown as { astrorderDesktop?: FileDesktop }).astrorderDesktop
}

export async function copyFilesToClipboard(
  paths: string[],
  options: { sessionId?: string; connectionId?: string | null } = {},
): Promise<boolean> {
  const cleanPaths = paths
    .map((raw) => raw.trim().replace(/:\d+(?::\d+)?$/, ''))
    .filter(Boolean)
  if (!cleanPaths.length) return false

  const id = notifications.show({
    loading: true,
    title: '正在复制文件',
    message: '正在准备文件句柄与剪贴板数据…',
    autoClose: false,
  })

  try {
    const res = await authenticatedApiFetch('/api/v1/files/stage', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        copy_to_clipboard: !fileDesktop()?.setClipboardFiles,
        paths: cleanPaths,
        session_id: options.sessionId,
        connection_id: options.connectionId || null,
      }),
    })

    if (!res.ok) {
      const errJson = await res.json().catch(() => ({}))
      throw new Error(errJson.detail || `暂存文件失败 (${res.status})`)
    }

    const data = await res.json()
    const localPaths: string[] = data.local_paths || []
    if (!localPaths.length) {
      throw new Error('未获取到有效物理文件路径')
    }

    if (!data.clipboard_set) {
      const desktopApi = fileDesktop()
      if (!desktopApi?.setClipboardFiles) throw new Error('当前环境无法写入文件剪贴板')
      const ok = await desktopApi.setClipboardFiles(localPaths)
      if (!ok) throw new Error('写入 Windows 剪贴板失败')
    }

    notifications.update({
      id,
      color: 'teal',
      title: '已复制到系统剪贴板',
      message: `已复制 ${localPaths.length} 个文件/文件夹，可直接在 Xftp 或资源管理器中按 Ctrl+V 传输！`,
      autoClose: 4000,
      loading: false,
    })
    return true
  } catch (err: unknown) {
    notifications.update({
      id,
      color: 'red',
      title: '复制文件失败',
      message: err instanceof Error ? err.message : '未知错误',
      autoClose: 5000,
      loading: false,
    })
    return false
  }
}

export async function downloadFile(url: string, filename: string): Promise<void> {
  const id = notifications.show({ loading: true, title: '正在下载文件', message: filename, autoClose: false })
  try {
    const native = fileDesktop()?.saveDownload
    if (native) {
      const saved = await native(url, filename)
      notifications.update({ id, loading: false, title: saved ? '文件已保存' : '已取消保存', message: filename, color: saved ? 'teal' : 'gray', autoClose: 3000 })
      return
    }
    const response = await authenticatedApiFetch(url)
    if (!response.ok) {
      const error = await response.json().catch(() => ({}))
      throw new Error(error.detail || `下载失败 (${response.status})`)
    }
    const blob = await response.blob()
    const address = URL.createObjectURL(blob)
    try {
      const link = document.createElement('a')
      link.href = address
      link.download = filename
      document.body.appendChild(link)
      link.click()
      link.remove()
    } finally {
      setTimeout(() => URL.revokeObjectURL(address), 60_000)
    }
    notifications.update({ id, loading: false, title: '已开始保存文件', message: filename, color: 'teal', autoClose: 3000 })
  } catch (error) {
    notifications.update({ id, loading: false, title: '下载文件失败', message: error instanceof Error ? error.message : '未知错误', color: 'red', autoClose: 5000 })
  }
}
