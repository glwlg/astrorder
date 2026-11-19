import type { BrowserDesignResponse, BrowserDesignState, DesignElement } from './browserDesign'

export function designSelectionBox(rect: DesignElement['rect'], viewport: BrowserDesignState['viewport'], image: { width: number; height: number }) {
  if (![rect.x, rect.y, rect.width, rect.height, viewport.width, viewport.height, image.width, image.height].every(Number.isFinite) ||
    rect.width <= 0 || rect.height <= 0 || viewport.width <= 0 || viewport.height <= 0 || image.width <= 0 || image.height <= 0) {
    throw new Error('选区尺寸无效，请重新选取页面元素')
  }
  if (rect.x + rect.width <= 0 || rect.y + rect.height <= 0 || rect.x >= viewport.width || rect.y >= viewport.height) {
    throw new Error('选中的元素已离开预览范围，请重新选取')
  }
  const scaleX = image.width / viewport.width
  const scaleY = image.height / viewport.height
  const lineWidth = Math.max(3, 3 * Math.min(scaleX, scaleY))
  const inset = lineWidth / 2
  const left = Math.max(inset, (rect.x - 4) * scaleX)
  const top = Math.max(inset, (rect.y - 4) * scaleY)
  const right = Math.min(image.width - inset, (rect.x + rect.width + 4) * scaleX)
  const bottom = Math.min(image.height - inset, (rect.y + rect.height + 4) * scaleY)
  return { x: left, y: top, width: right - left, height: bottom - top, lineWidth }
}

export async function designPreviewFile({ state, snapshot }: BrowserDesignResponse): Promise<File> {
  const bytes = Uint8Array.from(atob(snapshot.screenshot), character => character.charCodeAt(0))
  const original = new Blob([bytes], { type: snapshot.mime_type })
  if (!state.selected) return new File([original], '界面调整预览.jpg', { type: snapshot.mime_type })
  const image = await createImageBitmap(original)
  try {
    const canvas = document.createElement('canvas')
    canvas.width = image.width
    canvas.height = image.height
    const context = canvas.getContext('2d')
    if (!context) throw new Error('无法标记预览图，请重新打开调整界面')
    const box = designSelectionBox(state.selected.rect, state.viewport, image)
    context.drawImage(image, 0, 0)
    context.strokeStyle = '#ef233c'
    context.lineWidth = box.lineWidth
    context.strokeRect(box.x, box.y, box.width, box.height)
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(value => value ? resolve(value) : reject(new Error('预览图生成失败，请重试')), 'image/jpeg', 0.9)
    })
    return new File([blob], '界面调整预览.jpg', { type: 'image/jpeg' })
  } finally {
    image.close()
  }
}
