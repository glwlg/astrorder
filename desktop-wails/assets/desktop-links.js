export function installExternalLinks(win, openExternal) {
  const externalURL = (value) => {
    try {
      const url = new URL(value, win.location.href)
      return /^https?:$/.test(url.protocol) && url.origin !== win.location.origin ? url.href : null
    } catch { return null }
  }
  const open = (url) => {
    Promise.resolve().then(() => openExternal(url)).catch(error => {
      win.console.error('无法打开默认浏览器', error)
      win.alert('无法打开默认浏览器，请稍后重试。')
    })
  }
  const onClick = (event) => {
    if (event.defaultPrevented || (event.type === 'auxclick' ? event.button !== 1 : event.button !== 0)) return
    const anchor = event.target?.closest?.('a[href]')
    if (!anchor || anchor.hasAttribute('download')) return
    const url = externalURL(anchor.href)
    if (!url) return
    event.preventDefault()
    open(url)
  }
  win.document.addEventListener('click', onClick)
  win.document.addEventListener('auxclick', onClick)
  const nativeOpen = win.open.bind(win)
  win.open = (url, ...args) => {
    const external = externalURL(url)
    if (!external) return nativeOpen(url, ...args)
    open(external)
    return null
  }
}
