import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'

afterEach(() => vi.restoreAllMocks())
const streamResponse = (text: string) => new Response(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode(text)); controller.close() } }), { headers: { 'Content-Type': 'application/x-ndjson' } })

describe('Agent upgrade stream', () => {
  it('requires execution completion instead of treating a version probe as exit zero', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ status: 'verified', version: '0.162.1', up_to_date: false }), { headers: { 'Content-Type': 'application/json' } }))
    await expect(api.upgradeAgentStream('codex', vi.fn())).rejects.toThrow('升级接口未返回执行结果')
  })
  it('reads the final frame without a trailing newline', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(streamResponse('{"type":"init","command":"codex upgrade"}\n{"type":"chunk","data":"更新完成\\n"}\n{"type":"done","ok":true,"exit_code":0}'))
    const onChunk = vi.fn(), onInit = vi.fn()
    await expect(api.upgradeAgentStream('codex', onChunk, onInit)).resolves.toEqual({ ok: true, exit_code: 0 })
    expect(onChunk).toHaveBeenCalledWith('更新完成\n')
    expect(onInit).toHaveBeenCalledWith('codex upgrade')
  })
  it('surfaces execution errors instead of swallowing them as output', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(streamResponse('{"type":"error","error":"WSL: npm 不可用"}\n'))
    await expect(api.upgradeAgentStream('wsl', vi.fn())).rejects.toThrow('WSL: npm 不可用')
  })
  it('rejects truncated streams and preserves HTTP failure details', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(streamResponse('{"type":"chunk","data":"working"}\n')).mockResolvedValueOnce(new Response(JSON.stringify({ detail: 'SSH executable probe failed: permission denied' }), { status: 502, statusText: 'Bad Gateway' }))
    await expect(api.upgradeAgentStream('codex', vi.fn())).rejects.toThrow('升级流已结束，但未收到完成结果')
    await expect(api.upgradeAgentStream('wsl', vi.fn())).rejects.toThrow('permission denied')
  })
})
