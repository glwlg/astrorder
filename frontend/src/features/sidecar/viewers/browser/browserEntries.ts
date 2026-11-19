export function normalizeBrowserUrl(input: string): string {
  const value = input.trim()
  if (/^https?:\/\//i.test(value)) return value
  const local = /^(localhost|127(?:\.\d{1,3}){3}|\[::1\]|0\.0\.0\.0)(:\d+)?(\/|$)/i.test(value)
  return `${local ? 'http' : 'https'}://${value}`
}

export function browserEntryUrls(texts: string[]): string[] {
  const urls: string[] = []
  for (const text of [...texts].reverse()) {
    for (const match of text.matchAll(/https?:\/\/[^\s<>"'`，。；！）】]+/gi)) {
      const raw = match[0].replace(/[),.;\]}]+$/, '')
      let url: URL
      try { url = new URL(raw) } catch { continue }
      if (url.username || url.password) continue
      if (!urls.includes(url.href)) urls.push(url.href)
      if (urls.length === 6) return urls
    }
  }
  return urls
}
