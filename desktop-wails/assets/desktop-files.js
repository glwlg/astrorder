export function installFilePaths(win) {
  const paths = new WeakMap()
  const queue = []
  let active = null
  let nextId = 1
  const originalDrop = win._wails?.handlePlatformFileDrop
  if (win._wails) win._wails.handlePlatformFileDrop = (filenames, x, y) => {
    if (y === -987654 && x === active?.id) finish(filenames[0] || '')
    else if (y !== -987654) originalDrop?.(filenames, x, y)
  }
  const pump = () => {
    if (active || !queue.length) return
    active = queue.shift()
    active.id = -(nextId++)
    active.timer = win.setTimeout(() => {
      finish('', new Error('无法取得附件原始路径，请刷新页面后重新选择文件'))
    }, 5000)
    try {
      win.chrome.webview.postMessageWithAdditionalObjects(`file:drop:${active.id}:-987654`, [active.file])
    } catch (error) {
      // Clipboard/synthetic Files have bytes but no OS path; WebView2 rejects
      // those additional objects instead of sending an empty-path callback.
      if (error instanceof TypeError && String(error.message).includes('not a file on the disk')) finish('')
      else finish('', error)
    }
  }
  const finish = (path, error) => {
    if (!active) return
    const current = active
    active = null
    win.clearTimeout(current.timer)
    if (error) current.reject(error)
    else current.resolve(path)
    pump()
  }
  return file => {
    if (!win.chrome?.webview?.postMessageWithAdditionalObjects || !win._wails) return Promise.resolve('')
    if (!paths.has(file)) {
      const promise = new Promise((resolve, reject) => queue.push({ file, resolve, reject }))
      paths.set(file, promise)
      try { pump() } catch (error) { finish('', error) }
    }
    return paths.get(file)
  }
}

export async function resolvePreviewImages(images, win) {
  if (!Array.isArray(images) || images.length === 0 || images.length > 32) throw new Error('无效的预览图片列表')
  return Promise.all(images.map(async source => {
    const url = new URL(source, win.location.origin)
    if (url.protocol === 'data:') {
      if (!source.startsWith('data:image/') || source.length > 32 * 1024 * 1024) throw new Error('无效的图片数据')
      return source
    }
    if (url.origin !== win.location.origin || !['http:', 'https:'].includes(url.protocol)) throw new Error('不允许预览跨来源图片')
    const headers = new win.Headers()
    const token = win.localStorage?.getItem('astrorder:token')
    if (token) headers.set('Authorization', `Bearer ${token}`)
    const response = await win.fetch(url.href, { headers, credentials: 'same-origin', signal: AbortSignal.timeout(15000) })
    if (!response.ok) throw new Error(`图片读取失败 (${response.status})`)
    const blob = await response.blob()
    if (!blob.type.startsWith('image/') || blob.size > 24 * 1024 * 1024) throw new Error('无效或过大的图片')
    return new Promise((resolve, reject) => {
      const reader = new win.FileReader()
      reader.onload = () => resolve(reader.result)
      reader.onerror = () => reject(new Error('图片读取失败'))
      reader.readAsDataURL(blob)
    })
  }))
}
