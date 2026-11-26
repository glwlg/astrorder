export const AUDIO_MIME_TYPES: Record<string, string> = {
  '.wav': 'audio/wav',
  '.mp3': 'audio/mpeg',
  '.m4a': 'audio/mp4',
  '.ogg': 'audio/ogg',
  '.flac': 'audio/flac',
  '.aac': 'audio/aac',
  '.opus': 'audio/opus',
}

export function audioMediaType(name: string): string | undefined {
  const clean = name.trim().toLowerCase().split(/[?#]/)[0]
  const extension = clean.match(/\.[^./\\]+$/)?.[0]
  return extension ? AUDIO_MIME_TYPES[extension] : undefined
}

export function audioFormatBadge(name: string): string {
  const clean = name.trim().toLowerCase().split(/[?#]/)[0]
  const extension = clean.match(/\.([^./\\]+)$/)?.[1]
  return extension ? extension.toUpperCase() : 'AUDIO'
}
