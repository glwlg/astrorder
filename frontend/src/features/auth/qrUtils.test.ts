import { describe, expect, it } from 'vitest'
import { buildQrCodeUrl, parseAndValidateQrContent } from './qrUtils'

describe('qrUtils', () => {
  const origin = 'http://192.168.1.100:30001'
  const validId = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
  const validSecret = 'fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210'

  it('builds QR code URL encoding only the origin, /auth/qr, and id/scan_secret in fragment', () => {
    const url = buildQrCodeUrl(origin, validId, validSecret)
    expect(url).toBe(`http://192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret}`)
    expect(url).not.toContain('token')
  })

  it('validates and parses a valid same-origin QR URL with hash fragment', () => {
    const input = `http://192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret}`
    const result = parseAndValidateQrContent(input, origin)
    expect(result).toEqual({
      valid: true,
      id: validId,
      scan_secret: validSecret,
    })
  })

  it('strictly rejects query parameters carrying credentials or tokens', () => {
    const queryInput = `http://192.168.1.100:30001/auth/qr?id=${validId}&scan_secret=${validSecret}`
    expect(parseAndValidateQrContent(queryInput, origin).valid).toBe(false)

    const tokenQueryInput = `http://192.168.1.100:30001/auth/qr?token=abc#id=${validId}&scan_secret=${validSecret}`
    expect(parseAndValidateQrContent(tokenQueryInput, origin).valid).toBe(false)
  })

  it('rejects userinfo in URL', () => {
    const input = `http://admin:pass@192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret}`
    expect(parseAndValidateQrContent(input, origin).valid).toBe(false)
  })

  it('rejects external origin strictly without jumping', () => {
    const input = `https://evil.example.com/auth/qr#id=${validId}&scan_secret=${validSecret}`
    const result = parseAndValidateQrContent(input, origin)
    expect(result.valid).toBe(false)
    if (!result.valid) {
      expect(result.reason).toContain('站点')
    }
  })

  it('rejects wrong pathname on same origin', () => {
    const input = `http://192.168.1.100:30001/chat#id=${validId}&scan_secret=${validSecret}`
    const result = parseAndValidateQrContent(input, origin)
    expect(result.valid).toBe(false)
  })

  it('rejects extraneous or duplicate fields in fragment', () => {
    const extraField = `http://192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret}&role=admin`
    expect(parseAndValidateQrContent(extraField, origin).valid).toBe(false)

    const duplicateId = `http://192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret}&id=${validId}`
    expect(parseAndValidateQrContent(duplicateId, origin).valid).toBe(false)
  })

  it('rejects missing id or scan_secret', () => {
    const missingSecret = `http://192.168.1.100:30001/auth/qr#id=${validId}`
    expect(parseAndValidateQrContent(missingSecret, origin).valid).toBe(false)

    const missingId = `http://192.168.1.100:30001/auth/qr#scan_secret=${validSecret}`
    expect(parseAndValidateQrContent(missingId, origin).valid).toBe(false)
  })

  it('rejects non-64-hex id or scan_secret', () => {
    const shortId = `http://192.168.1.100:30001/auth/qr#id=1234&scan_secret=${validSecret}`
    expect(parseAndValidateQrContent(shortId, origin).valid).toBe(false)

    const upperSecret = `http://192.168.1.100:30001/auth/qr#id=${validId}&scan_secret=${validSecret.toUpperCase()}`
    expect(parseAndValidateQrContent(upperSecret, origin).valid).toBe(false)
  })

  it('rejects non-url garbage text', () => {
    expect(parseAndValidateQrContent('not a url at all', origin).valid).toBe(false)
  })
})