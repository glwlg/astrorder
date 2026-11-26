export interface SubtitleCue {
  id: string
  index: number
  start: number
  end: number
  text: string
}

const TIMING_LINE_RE =
  /^\s*(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})\s*-->\s*(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})/

function toSeconds(hrs?: string, mins?: string, secs?: string, ms?: string): number {
  const h = hrs ? Number.parseInt(hrs, 10) : 0
  const m = mins ? Number.parseInt(mins, 10) : 0
  const s = secs ? Number.parseInt(secs, 10) : 0
  const frac = ms ? Number.parseInt(ms.padEnd(3, '0').slice(0, 3), 10) / 1000 : 0
  return h * 3600 + m * 60 + s + frac
}

function stripSubtitleMarkup(raw: string): string {
  return raw
    .replace(/<[^>]+>/g, '')
    .replace(/\{\\[^}]*\}/g, '')
    .trim()
}

export function parseSubtitleText(raw: string): SubtitleCue[] {
  if (!raw || !raw.trim()) return []
  const cleaned = raw.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n')
  const blocks = cleaned.split(/\n{2,}/)
  const cues: SubtitleCue[] = []

  for (const block of blocks) {
    const lines = block
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0)
    if (lines.length === 0) continue
    if (/^WEBVTT\b/i.test(lines[0]) || /^NOTE\b/i.test(lines[0]) || /^STYLE\b/i.test(lines[0])) {
      continue
    }

    let timingIdx = -1
    for (let i = 0; i < Math.min(lines.length, 3); i += 1) {
      if (TIMING_LINE_RE.test(lines[i])) {
        timingIdx = i
        break
      }
    }
    if (timingIdx < 0) continue

    const match = lines[timingIdx].match(TIMING_LINE_RE)
    if (!match) continue
    const start = toSeconds(match[1], match[2], match[3], match[4])
    const end = toSeconds(match[5], match[6], match[7], match[8])
    const textLines = lines
      .slice(timingIdx + 1)
      .map(stripSubtitleMarkup)
      .filter((line) => line.length > 0)
    if (textLines.length === 0) continue

    const idx = cues.length + 1
    cues.push({
      id: `cue-${idx}-${Math.round(start * 1000)}`,
      index: idx,
      start,
      end: Math.max(end, start + 0.1),
      text: textLines.join('\n'),
    })
  }

  return cues
}

function formatVttTimestamp(seconds: number): string {
  const safe = Math.max(0, Number.isFinite(seconds) ? seconds : 0)
  const hrs = Math.floor(safe / 3600)
  const mins = Math.floor((safe % 3600) / 60)
  const secs = Math.floor(safe % 60)
  const ms = Math.round((safe - Math.floor(safe)) * 1000)
  const pad2 = (n: number) => String(n).padStart(2, '0')
  const pad3 = (n: number) => String(n).padStart(3, '0')
  return `${pad2(hrs)}:${pad2(mins)}:${pad2(secs)}.${pad3(Math.min(ms, 999))}`
}

export function cuesToWebVtt(cues: SubtitleCue[]): string {
  const lines = ['WEBVTT', '']
  for (const cue of cues) {
    lines.push(`${formatVttTimestamp(cue.start)} --> ${formatVttTimestamp(cue.end)}`)
    lines.push(cue.text)
    lines.push('')
  }
  return lines.join('\n')
}

export function formatTimecode(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '00:00.00'
  const totalCs = Math.round(seconds * 100)
  const cs = totalCs % 100
  const totalSec = Math.floor(totalCs / 100)
  const hrs = Math.floor(totalSec / 3600)
  const mins = Math.floor((totalSec % 3600) / 60)
  const secs = totalSec % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return hrs > 0
    ? `${hrs}:${pad(mins)}:${pad(secs)}.${pad(cs)}`
    : `${pad(mins)}:${pad(secs)}.${pad(cs)}`
}

export function findActiveCueIndex(cues: SubtitleCue[], currentTime: number): number {
  for (let i = 0; i < cues.length; i += 1) {
    if (currentTime >= cues[i].start - 0.05 && currentTime <= cues[i].end + 0.05) {
      return i
    }
  }
  return -1
}

export function formatFrameFileName(videoName: string, currentTime: number): string {
  const stem = videoName.replace(/\.[^./\\]+$/, '').replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]+/g, '_') || 'frame'
  const tc = formatTimecode(currentTime).replace(/:/g, 'm').replace('.', 's')
  return `${stem}_${tc}.png`
}

export async function captureVideoFrameAsPng(
  video: HTMLVideoElement,
  videoName: string,
  currentTime: number,
  maxWidth = 1920,
): Promise<{ file: File; dataUrl: string } | null> {
  const naturalW = video.videoWidth
  const naturalH = video.videoHeight
  if (!naturalW || !naturalH) return null

  const scale = naturalW > maxWidth ? maxWidth / naturalW : 1
  const targetW = Math.max(1, Math.round(naturalW * scale))
  const targetH = Math.max(1, Math.round(naturalH * scale))

  const canvas = document.createElement('canvas')
  canvas.width = targetW
  canvas.height = targetH
  const ctx = canvas.getContext('2d')
  if (!ctx) return null

  try {
    ctx.drawImage(video, 0, 0, targetW, targetH)
  } catch {
    return null
  }

  let dataUrl = ''
  try {
    dataUrl = canvas.toDataURL('image/png')
  } catch {
    return null
  }

  const blob = await new Promise<Blob | null>((resolve) => {
    if (typeof canvas.toBlob === 'function') {
      canvas.toBlob((b) => resolve(b), 'image/png')
    } else {
      resolve(null)
    }
  })

  if (!blob) {
    // Fallback decode from dataUrl (useful in jsdom/older environments)
    const base64 = dataUrl.split(',')[1] || ''
    const binary = typeof atob === 'function' ? atob(base64) : ''
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
    const fallbackFile = new File([bytes], formatFrameFileName(videoName, currentTime), { type: 'image/png' })
    return { file: fallbackFile, dataUrl }
  }

  const file = new File([blob], formatFrameFileName(videoName, currentTime), { type: 'image/png' })
  return { file, dataUrl }
}
