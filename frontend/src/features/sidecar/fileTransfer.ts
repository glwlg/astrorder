import { notifications } from '@mantine/notifications'
import { authenticatedApiFetch } from '../../api/client'

type FileDesktop = {
  setClipboardFiles?: (paths: string[]) => Promise<boolean>
  saveDownload?: (url: string, filename: string) => Promise<boolean>
}
export function fileDesktop(): FileDesktop | undefined {
  return (window as unknown as { astrorderDesktop?: FileDesktop }).astrorderDesktop
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
