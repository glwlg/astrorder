export const VIDEO_MIME_TYPES: Record<string, string> = {
  '.mp4': 'video/mp4',
  '.m4v': 'video/mp4',
  '.webm': 'video/webm',
  '.mov': 'video/quicktime',
  '.ogv': 'video/ogg',
  '.mkv': 'video/x-matroska',
}

export function videoMediaType(name: string): string | undefined {
  const clean = name.trim().toLowerCase().split(/[?#]/)[0]
  const extension = clean.match(/\.[^./\\]+$/)?.[0]
  return extension ? VIDEO_MIME_TYPES[extension] : undefined
}

export function formatVideoDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return ''
  const total = Math.floor(seconds)
  const hrs = Math.floor(total / 3600)
  const mins = Math.floor((total % 3600) / 60)
  const secs = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return hrs > 0 ? `${hrs}:${pad(mins)}:${pad(secs)}` : `${pad(mins)}:${pad(secs)}`
}

export function buildAuthenticatedVideoUrl(readUrl: string, revision = 0): string {
  if (!readUrl) return ''
  const isHttp = /^https?:\/\//i.test(readUrl)
  if (isHttp) return readUrl
  try {
    const parsed = new URL(readUrl, typeof window !== 'undefined' ? window.location.origin : 'http://localhost')
    if (!parsed.searchParams.has('token')) {
      try {
        const token = localStorage.getItem('astrorder:token')
        if (token) parsed.searchParams.set('token', token)
      } catch {}
    }
    if (revision > 0) {
      parsed.searchParams.set('_r', String(revision))
    }
    return `${parsed.pathname}${parsed.search}`
  } catch {
    return readUrl
  }
}
