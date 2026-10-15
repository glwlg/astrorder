import type { Attachment } from './types'

/** Native Codex inline images are raster data; file links stay same-origin. */
export function attachmentHref(attachment: Attachment): string | undefined {
  if (attachment.media_type.startsWith('image/') && /^data:image\/(?:png|jpeg|gif|webp);base64,[A-Za-z0-9+/]+={0,2}$/.test(attachment.url)) {
    return attachment.url
  }
  try {
    const parsed = new URL(attachment.url, window.location.origin)
    if (parsed.origin !== window.location.origin) return undefined
    return `${parsed.pathname}${parsed.search}${parsed.hash}`
  } catch {
    return undefined
  }
}
