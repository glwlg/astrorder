self.addEventListener('push', event => {
  const notice = event.data?.json()
  if (!notice?.key || !notice.title || !notice.message || typeof notice.url !== 'string') return
  const url = new URL(notice.url, self.location.origin)
  if (url.origin !== self.location.origin) return
  event.waitUntil(self.registration.showNotification(notice.title, {
    body: notice.message,
    tag: notice.key,
    icon: '/pwa-192.png',
    data: { url: url.href },
  }))
})

self.addEventListener('notificationclick', event => {
  event.notification.close()
  const target = event.notification.data?.url
  if (typeof target !== 'string') return
  const url = new URL(target, self.location.origin)
  if (url.origin !== self.location.origin) return
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    const client = windows.find(window => new URL(window.url).origin === url.origin)
    if (client) {
      await client.navigate(url.href)
      await client.focus()
    } else {
      await self.clients.openWindow(url.href)
    }
  })())
})
