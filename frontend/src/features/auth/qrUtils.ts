import type { QrValidationResult } from './types'

export const HEX_64_REGEX = /^[0-9a-f]{64}$/

export function buildQrCodeUrl(origin: string, id: string, scan_secret: string): string {
  const params = new URLSearchParams({ id, scan_secret })
  return `${origin}/auth/qr#${params.toString()}`
}

export function parseAndValidateQrContent(content: string, currentOrigin: string): QrValidationResult {
  let parsed: URL
  try {
    parsed = new URL(content)
  } catch {
    return { valid: false, reason: '不是有效的二维码地址' }
  }

  // Reject userinfo
  if (parsed.username || parsed.password) {
    return { valid: false, reason: '二维码地址包含非法凭据信息' }
  }

  // Strictly same origin
  if (parsed.origin !== currentOrigin) {
    return { valid: false, reason: '仅支持扫描当前站点的授权二维码' }
  }

  // Strictly /auth/qr path
  if (parsed.pathname !== '/auth/qr') {
    return { valid: false, reason: '不是有效的星序登录授权二维码' }
  }

  // Reject query credentials or tokens
  if (parsed.searchParams.has('id') || parsed.searchParams.has('scan_secret') || parsed.searchParams.has('token')) {
    return { valid: false, reason: '禁止在查询参数中携带授权凭据' }
  }

  // Strictly accept fragment only
  if (!parsed.hash || !parsed.hash.startsWith('#')) {
    return { valid: false, reason: '二维码缺少授权凭据片段' }
  }

  const hashStr = parsed.hash.slice(1)
  const params = new URLSearchParams(hashStr)

  // Disallow extraneous or duplicate fields in fragment
  const keys = Array.from(params.keys())
  if (keys.length !== 2 || !params.has('id') || !params.has('scan_secret')) {
    return { valid: false, reason: '二维码授权片段参数无效或包含多余字段' }
  }

  if (params.getAll('id').length !== 1 || params.getAll('scan_secret').length !== 1) {
    return { valid: false, reason: '二维码授权片段包含重复参数' }
  }

  const id = params.get('id')
  const scanSecret = params.get('scan_secret')

  if (!id || !HEX_64_REGEX.test(id)) {
    return { valid: false, reason: '二维码凭据标识无效' }
  }

  if (!scanSecret || !HEX_64_REGEX.test(scanSecret)) {
    return { valid: false, reason: '二维码凭据密钥无效' }
  }

  return {
    valid: true,
    id,
    scan_secret: scanSecret,
  }
}

export function extractQrFragmentParams(): { id: string | null; scan_secret: string | null; error?: string } {
  if (typeof window === 'undefined') {
    return { id: null, scan_secret: null }
  }
  // Construct URL with canonical /auth/qr pathname to run through the exact same strict parser
  const targetUrl = `${window.location.origin}/auth/qr${window.location.search}${window.location.hash}`
  const result = parseAndValidateQrContent(targetUrl, window.location.origin)
  if (result.valid) {
    return { id: result.id, scan_secret: result.scan_secret }
  }
  return { id: null, scan_secret: null, error: result.reason }
}

export function clearQrFragmentFromHistory(): void {
  if (typeof window !== 'undefined' && window.location.hash) {
    const searchParams = new URLSearchParams(window.location.search)
    searchParams.delete('token')
    searchParams.delete('scan_secret')
    searchParams.delete('id')
    const search = searchParams.toString() ? `?${searchParams.toString()}` : ''
    window.history.replaceState(null, '', `${window.location.pathname}${search}`)
  }
}