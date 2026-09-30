import { expect, it } from 'vitest'
import { buildLocalVoiceStreamUrl, downsampleToPcm16 } from './localVoiceStream'

it('builds an authenticated same-origin local voice websocket URL', () => {
  localStorage.setItem('astrorder:token', 'a token')
  const url = buildLocalVoiceStreamUrl()
  expect(url).toContain('/api/v1/audio/local-stream')
  expect(url).toContain('token=a%20token')
})

it('downsamples browser audio to signed 16 kHz PCM', () => {
  const source = new Float32Array([1, 1, 1, -1, -1, -1])
  const pcm = downsampleToPcm16(source, 48000)
  expect(Array.from(pcm)).toEqual([32767, -32768])
})

it('clamps samples outside the PCM range', () => {
  const pcm = downsampleToPcm16(new Float32Array([2, -2, 0]), 16000)
  expect(Array.from(pcm)).toEqual([32767, -32768, 0])
})
