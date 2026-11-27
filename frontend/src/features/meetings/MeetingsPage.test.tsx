import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { ActionInboxView } from '../inbox/ActionInboxView'
import { AiProvidersSettingsSection } from '../services/AiProvidersSettingsSection'
import { MeetingFloatingCapsule } from './MeetingFloatingCapsule'
import { useMeetingSessionStore } from './meetingSessionStore'
import { MeetingsPage } from './MeetingsPage'

class MockWebSocket {
  static OPEN = 1
  static CONNECTING = 0
  static CLOSED = 3
  static instances: MockWebSocket[] = []

  url: string
  binaryType = 'arraybuffer'
  readyState = MockWebSocket.OPEN
  sent: Array<string | ArrayBuffer> = []
  closed = false
  onmessage: ((ev: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
    queueMicrotask(() => {
      this.onmessage?.({ data: JSON.stringify({ type: 'ready' }) })
    })
  }

  send(data: string | ArrayBuffer) {
    this.sent.push(data)
  }

  close() {
    this.closed = true
    this.readyState = MockWebSocket.CLOSED
  }

  emitMessage(payload: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

function installLiveAudioMocks() {
  MockWebSocket.instances = []
  const trackStop = vi.fn()
  const mockStream = {
    getTracks: () => [{ stop: trackStop }],
  } as unknown as MediaStream

  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: {
      getUserMedia: vi.fn().mockResolvedValue(mockStream),
      enumerateDevices: vi.fn().mockResolvedValue([
        { kind: 'audioinput', deviceId: 'default', label: '测试麦克风' },
      ]),
    },
  })

  class MockAudioContext {
    sampleRate = 16000
    destination = {}
    createMediaStreamSource() {
      return { connect: vi.fn() }
    }
    createAnalyser() {
      return {
        fftSize: 64,
        smoothingTimeConstant: 0.72,
        frequencyBinCount: 32,
        getByteFrequencyData: (arr: Uint8Array) => arr.fill(80),
      }
    }
    createScriptProcessor() {
      return {
        onaudioprocess: null,
        connect: vi.fn(),
        disconnect: vi.fn(),
      }
    }
    createGain() {
      return {
        gain: { value: 0 },
        connect: vi.fn(),
      }
    }
    close() {
      return Promise.resolve()
    }
  }

  vi.stubGlobal('AudioContext', MockAudioContext)
  vi.stubGlobal('WebSocket', MockWebSocket)
  return { trackStop }
}

function RouteSwitcherShell() {
  const navigate = useNavigate()
  return (
    <>
      <button type="button" onClick={() => navigate('/chat')}>
        去会话页
      </button>
      <button type="button" onClick={() => navigate('/meetings')}>
        去会议页
      </button>
      <Routes>
        <Route path="/meetings" element={<MeetingsPage />} />
        <Route path="/chat" element={<div>会话主视图</div>} />
      </Routes>
      <MeetingFloatingCapsule />
    </>
  )
}

describe('MeetingsPage v2 real-time studio, persistent capsule & ActionInbox loop', () => {
  afterEach(() => {
    useMeetingSessionStore.getState()._resetForTests()
    delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
    cleanup()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('opens summary model config popover, reuses configured AI provider & model, and summarizes meeting content', async () => {
    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
      id: 'r2t2-local-voice',
      name: 'R2T2 本地流式听写',
      enabled: true,
      state: 'running',
      available: true,
      progress: null,
      error: null,
      pid: 1234,
      device: 'Vulkan0',
      model: 'Confucius4-R2T2-Q8_0.gguf',
    })
    vi.spyOn(api, 'getAiProviders').mockResolvedValue({
      providers: [
        {
          id: 'prv_magpie',
          name: '公司 Magpie 网关',
          protocol: 'openai',
          vendor: 'magpie',
          base_url: 'https://magpie.example.com/v1',
          api_key_set: true,
          masked_key: 'sk-mag...1234',
          models: ['guolian-llm-max', 'qwen-plus'],
          enabled: true,
          management_url: 'https://magpie.example.com',
        },
        {
          id: 'prv_gemini',
          name: 'Google Gemini',
          protocol: 'gemini',
          vendor: '',
          base_url: 'https://generativelanguage.googleapis.com/v1beta',
          api_key_set: true,
          masked_key: 'AIza...9999',
          models: ['gemini-2.5-flash'],
          enabled: true,
        },
      ],
      default_provider_id: 'prv_magpie',
      default_model: 'guolian-llm-max',
      default_reasoning: 'low',
      effective_configured: true,
      effective_model: 'guolian-llm-max',
      effective_provider_name: '公司 Magpie 网关',
    })
    vi.spyOn(api, 'getMeetingSettings').mockResolvedValue({
      model_source: 'global',
      provider_id: '',
      base_url: 'https://magpie.example.com/v1',
      api_key_set: false,
      model: 'guolian-llm-max',
      auto_summarize: true,
      auto_summarize_interval_sec: 30,
      summary_style: 'concise',
      effective_model: 'guolian-llm-max',
      effective_provider_name: '公司 Magpie 网关',
      effective_configured: true,
      global_model: 'guolian-llm-max',
      global_provider_name: '公司 Magpie 网关',
      global_configured: true,
    })
    const saveSettingsSpy = vi.spyOn(api, 'saveMeetingSettings').mockResolvedValue({
      model_source: 'provider',
      provider_id: 'prv_gemini',
      provider_name: 'Google Gemini',
      base_url: '',
      api_key_set: false,
      model: 'gemini-2.5-flash',
      auto_summarize: true,
      auto_summarize_interval_sec: 20,
      summary_style: 'technical',
      effective_model: 'gemini-2.5-flash',
      effective_provider_name: 'Google Gemini',
      effective_configured: true,
      global_model: 'guolian-llm-max',
      global_provider_name: '公司 Magpie 网关',
      global_configured: true,
    })
    vi.spyOn(api, 'summarizeMeeting').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'gemini-2.5-flash',
      title: 'Q4 发布评审会',
      summary: '确认周五发布并启用 R2T2 实时转写。',
      topics: [{ timestamp: '00:00', title: '发布与转写方案', detail: '统一使用本地 R2T2。' }],
      decisions: ['周五 18:00 发布'],
      risks: ['压测环境待确认'],
      action_items: [
        {
          id: 'act_1',
          meeting_id: '',
          meeting_title: 'Q4 发布评审会',
          text: '输出客户演示 PPT',
          owner: '张三',
          due_date: '2026-11-28',
          priority: 'high',
          status: 'open',
        },
      ],
      minutes_markdown: '# Q4 发布评审会\n\n确认周五发布。',
      assembled_prompt: 'prompt',
    })
    const saveMeetingSpy = vi.spyOn(api, 'saveMeeting').mockResolvedValue({
      id: 'mtg_1',
      title: 'Q4 发布评审会',
      meeting_date: '2026-11-25',
      minutes_markdown: '# Q4 发布评审会\n\n确认周五发布。',
      action_items: [
        {
          id: 'act_1',
          meeting_id: 'mtg_1',
          meeting_title: 'Q4 发布评审会',
          text: '输出客户演示 PPT',
          owner: '张三',
          due_date: '2026-11-28',
          priority: 'high',
          status: 'open',
        },
      ],
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/meetings']}>
          <MantineProvider>
            <MeetingsPage />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    expect(screen.getByRole('button', { name: '开始实时录制' })).toBeTruthy()
    await waitFor(() => {
      expect(screen.getByText('本地语音转写已就绪')).toBeTruthy()
    })
    expect(screen.queryByText(/R2T2/)).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '配置会议总结模型' }))
    fireEvent.click(screen.getByRole('tab', { name: '指定模型' }))
    await waitFor(() => {
      expect(screen.getByLabelText('选择 AI 供应商')).toBeTruthy()
    })
    fireEvent.change(screen.getByLabelText('选择 AI 供应商'), { target: { value: 'prv_gemini' } })
    fireEvent.change(screen.getByLabelText('会议总结模型名称'), { target: { value: 'gemini-2.5-flash' } })
    fireEvent.click(screen.getByRole('button', { name: '20s' }))
    fireEvent.click(screen.getByRole('button', { name: '技术评审' }))
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() => {
      expect(saveSettingsSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          model_source: 'provider',
          provider_id: 'prv_gemini',
          model: 'gemini-2.5-flash',
          auto_summarize_interval_sec: 20,
          summary_style: 'technical',
        }),
      )
    })

    fireEvent.click(screen.getByRole('button', { name: '粘贴记录' }))
    fireEvent.change(screen.getByPlaceholderText('粘贴会议记录或转写文本…'), {
      target: { value: '[00:00] 确认周五 18:00 发布，张三负责输出客户演示 PPT。' },
    })
    fireEvent.click(screen.getByRole('button', { name: '提炼纪要' }))

    await waitFor(() => {
      expect(screen.getByText('确认周五发布并启用 R2T2 实时转写。')).toBeTruthy()
      expect(screen.getByText('发布与转写方案')).toBeTruthy()
      expect(screen.getByText('输出客户演示 PPT')).toBeTruthy()
    })

    // Auto-saved without manual save button
    await waitFor(() => {
      expect(saveMeetingSpy).toHaveBeenCalled()
      expect(screen.getByText('已自动保存')).toBeTruthy()
    })

    // Full speech-to-text transcript viewer tab works in both timestamped and continuous plain modes
    fireEvent.click(screen.getByRole('tab', { name: '转写原文' }))
    const rawPane = screen.getByLabelText('录音转文字原文')
    expect(within(rawPane).getByText(/确认周五 18:00 发布/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '连续纯文本' }))
    expect(within(rawPane).getByText(/张三负责输出客户演示 PPT/)).toBeTruthy()

    // Completed meeting only offers starting a brand-new recording (no resuming into finished meeting)
    expect(screen.queryByRole('button', { name: '继续实时录制' })).toBeNull()
    expect(screen.getByRole('button', { name: '开始新会议录制' })).toBeTruthy()
  })

  it('guarantees immediate auto-save + final full re-summary even when incremental summary is in flight, and starts fresh on next record', async () => {
    installLiveAudioMocks()
    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
      id: 'r2t2-local-voice',
      name: 'R2T2 本地流式听写',
      enabled: true,
      state: 'running',
      available: true,
      progress: null,
      error: null,
      pid: 1234,
      device: 'Vulkan0',
      model: 'Confucius4-R2T2-Q8_0.gguf',
    })
    vi.spyOn(api, 'getMeetingSettings').mockResolvedValue({
      model_source: 'global',
      base_url: 'https://api.deepseek.com/v1',
      api_key_set: false,
      model: 'deepseek-chat',
      auto_summarize: false,
      auto_summarize_interval_sec: 30,
      summary_style: 'concise',
      effective_model: 'deepseek-chat',
      effective_configured: true,
      global_model: 'deepseek-chat',
      global_configured: true,
    })

    let resolveIncremental!: (val: Awaited<ReturnType<typeof api.summarizeMeeting>>) => void
    const summarizeSpy = vi
      .spyOn(api, 'summarizeMeeting')
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveIncremental = resolve
          }),
      )
      .mockResolvedValueOnce({
        ok: true,
        mode: 'llm',
        llm_configured: true,
        model: 'deepseek-chat',
        title: '整体总结后的最终会议标题',
        summary: '完整会议最终总结。',
        topics: [{ timestamp: '00:00', title: '最终完整议题', detail: '全部讨论已通盘整合。' }],
        decisions: ['最终决议'],
        risks: [],
        action_items: [],
        minutes_markdown: '# 整体总结后的最终会议标题\n\n完整会议最终总结。',
        assembled_prompt: 'p',
      })

    const saveSpy = vi.spyOn(api, 'saveMeeting').mockImplementation(async (payload) => ({
      id: payload.id || 'mtg_auto_1',
      title: payload.title || '会议纪要',
      meeting_date: payload.meeting_date || '2026-11-25',
      duration_seconds: payload.duration_seconds || 0,
      transcript: payload.transcript || '',
      segments: payload.segments || [],
      summary: payload.summary || '',
      minutes_markdown: payload.minutes_markdown || '',
      action_items: payload.action_items || [],
    }))

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/meetings']}>
          <MantineProvider>
            <MeetingsPage />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('本地语音转写已就绪')).toBeTruthy()
    })
    expect(screen.queryByText(/R2T2/)).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '开始实时录制' }))
    await waitFor(() => {
      expect(useMeetingSessionStore.getState().recordState).toBe('recording')
    })

    const ws1 = MockWebSocket.instances[0]
    act(() => {
      ws1.emitMessage({ type: 'segment', text: '第一段发言内容讨论上线排期' })
    })
    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalled()
    })

    // Start an in-flight incremental summary right before stopping
    const incPromise = useMeetingSessionStore.getState().runSummarize({ incremental: true })

    // Stop recording while incremental summary is STILL running
    fireEvent.click(screen.getByRole('button', { name: '结束' }))
    act(() => {
      ws1.emitMessage({ type: 'done', text: '第二段结尾发言确认周五发布' })
    })

    // Now finish the earlier in-flight incremental summary -> final full summary MUST run right after!
    act(() => {
      resolveIncremental({
        ok: true,
        mode: 'llm',
        llm_configured: true,
        model: 'deepseek-chat',
        title: '临时标题',
        summary: '增量草稿',
        topics: [],
        decisions: [],
        risks: [],
        action_items: [],
        minutes_markdown: '# 临时',
        assembled_prompt: 'p',
      })
    })
    await incPromise

    await waitFor(() => {
      expect(summarizeSpy).toHaveBeenCalledTimes(2)
      expect(summarizeSpy).toHaveBeenLastCalledWith(
        expect.objectContaining({
          incremental: false,
          transcript: expect.stringContaining('第二段结尾发言确认周五发布'),
        }),
      )
      expect(screen.getByText('完整会议最终总结。')).toBeTruthy()
    })

    // Clicking "开始新会议录制" resets completed meeting & starts brand-new recording
    fireEvent.click(screen.getByRole('button', { name: '开始新会议录制' }))
    await waitFor(() => {
      expect(useMeetingSessionStore.getState().recordState).toBe('recording')
      expect(useMeetingSessionStore.getState().activeId).toBe('')
      expect(useMeetingSessionStore.getState().segments.length).toBe(0)
      expect(MockWebSocket.instances.length).toBe(2)
    })
  })

  it('keeps recording alive across route switches and controls it via the floating capsule', async () => {
    const { trackStop } = installLiveAudioMocks()
    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
      id: 'r2t2-local-voice',
      name: 'R2T2 本地流式听写',
      enabled: true,
      state: 'running',
      available: true,
      progress: null,
      error: null,
      pid: 1234,
      device: 'Vulkan0',
      model: 'Confucius4-R2T2-Q8_0.gguf',
    })
    vi.spyOn(api, 'getMeetingSettings').mockResolvedValue({
      model_source: 'global',
      base_url: 'https://api.deepseek.com/v1',
      api_key_set: false,
      model: 'deepseek-chat',
      auto_summarize: false,
      auto_summarize_interval_sec: 30,
      summary_style: 'concise',
      effective_model: 'deepseek-chat',
      effective_configured: true,
      global_model: 'deepseek-chat',
      global_configured: true,
    })
    const summarizeSpy = vi.spyOn(api, 'summarizeMeeting').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'deepseek-chat',
      title: '切换页面测试会议',
      summary: '全程无中断转写。',
      topics: [],
      decisions: [],
      risks: [],
      action_items: [],
      minutes_markdown: '# 切换页面测试会议',
      assembled_prompt: 'p',
    })
    vi.spyOn(api, 'saveMeeting').mockResolvedValue({
      id: 'mtg_live',
      title: '切换页面测试会议',
      meeting_date: '2026-11-25',
      minutes_markdown: '# 切换页面测试会议',
      action_items: [],
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/meetings']}>
          <MantineProvider>
            <RouteSwitcherShell />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('本地语音转写已就绪')).toBeTruthy()
    })
    expect(screen.queryByText(/R2T2/)).toBeNull()

    // Start recording on /meetings
    fireEvent.click(screen.getByRole('button', { name: '开始实时录制' }))
    await waitFor(() => {
      expect(useMeetingSessionStore.getState().recordState).toBe('recording')
    })

    const ws = MockWebSocket.instances[0]
    expect(ws).toBeTruthy()
    act(() => {
      ws.emitMessage({ type: 'transcript', text: '各位好，现在开始讨论发布计划' })
    })
    await waitFor(() => {
      expect(screen.getByText('各位好，现在开始讨论发布计划')).toBeTruthy()
    })

    // Navigate away to /chat: MeetingsPage unmounts, but recording MUST stay alive!
    fireEvent.click(screen.getByRole('button', { name: '去会话页' }))
    expect(screen.getByText('会话主视图')).toBeTruthy()
    expect(useMeetingSessionStore.getState().recordState).toBe('recording')
    expect(ws.closed).toBe(false)
    expect(trackStop).not.toHaveBeenCalled()

    // In-app floating capsule appears automatically when away from /meetings
    const capsule = screen.getByRole('region', { name: '独立录音胶囊' })
    expect(capsule).toBeTruthy()
    expect(capsule.textContent).toContain('各位好，现在开始讨论发布计划')

    // Click Expand on floating capsule -> navigates right back to /meetings with live session intact
    fireEvent.click(screen.getByRole('button', { name: '展开会议页面' }))
    await waitFor(() => {
      expect(screen.getByRole('region', { name: '实时录制控制栏' })).toBeTruthy()
      expect(screen.getByText('各位好，现在开始讨论发布计划')).toBeTruthy()
    })

    // Navigate away again and stop directly from floating capsule
    fireEvent.click(screen.getByRole('button', { name: '去会话页' }))
    fireEvent.click(screen.getByRole('button', { name: '结束录制' }))
    expect(ws.sent).toContain(JSON.stringify({ type: 'stop' }))
    expect(trackStop).toHaveBeenCalled()

    act(() => {
      ws.emitMessage({ type: 'done', text: '各位好，现在开始讨论发布计划' })
    })

    await waitFor(() => {
      expect(useMeetingSessionStore.getState().recordState).toBe('idle')
      expect(screen.queryByRole('region', { name: '独立录音胶囊' })).toBeNull()
    })
    await waitFor(() => {
      expect(summarizeSpy).toHaveBeenCalled()
    })
  })

  it('pops out native desktop capsule on start and handles stop actions from desktop window', async () => {
    installLiveAudioMocks()
    const showMeetingCapsule = vi.fn().mockResolvedValue(undefined)
    const updateMeetingCapsule = vi.fn().mockResolvedValue(undefined)
    const hideMeetingCapsule = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {
      showMeetingCapsule,
      updateMeetingCapsule,
      hideMeetingCapsule,
    }

    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
      id: 'r2t2-local-voice',
      name: 'R2T2 本地流式听写',
      enabled: true,
      state: 'running',
      available: true,
      progress: null,
      error: null,
      pid: 1234,
      device: 'Vulkan0',
      model: 'Confucius4-R2T2-Q8_0.gguf',
    })
    vi.spyOn(api, 'getMeetingSettings').mockResolvedValue({
      model_source: 'global',
      base_url: 'https://api.deepseek.com/v1',
      api_key_set: false,
      model: 'deepseek-chat',
      auto_summarize: false,
      auto_summarize_interval_sec: 30,
      summary_style: 'concise',
      effective_model: 'deepseek-chat',
      effective_configured: true,
      global_model: 'deepseek-chat',
      global_configured: true,
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/meetings']}>
          <MantineProvider>
            <RouteSwitcherShell />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('本地语音转写已就绪')).toBeTruthy()
    })
    expect(screen.queryByText(/R2T2/)).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '开始实时录制' }))
    await waitFor(() => {
      expect(showMeetingCapsule).toHaveBeenCalledWith(
        expect.objectContaining({ active: true, paused: false }),
      )
    })

    // Navigate away: since native desktop capsule is active, in-app duplicate capsule stays hidden
    fireEvent.click(screen.getByRole('button', { name: '去会话页' }))
    expect(screen.queryByRole('region', { name: '独立录音胶囊' })).toBeNull()

    // Trigger stop action from native desktop capsule window
    const ws = MockWebSocket.instances[0]
    act(() => {
      window.dispatchEvent(new CustomEvent('astrorder:meeting-capsule-action', { detail: { action: 'stop' } }))
    })
    expect(ws.sent).toContain(JSON.stringify({ type: 'stop' }))
    act(() => {
      ws.emitMessage({ type: 'done', text: '' })
    })
    await waitFor(() => {
      expect(hideMeetingCapsule).toHaveBeenCalled()
    })
  })

  it('surfaces open meeting action items inside ActionInboxView and lets user complete them', async () => {
    vi.spyOn(api, 'listMeetingActionItems').mockResolvedValue({
      open_count: 1,
      items: [
        {
          id: 'act_9',
          meeting_id: 'mtg_9',
          meeting_title: '周例会',
          text: '核对季度预算表',
          owner: '李四',
          due_date: '2026-11-27',
          priority: 'high',
          status: 'open',
        },
      ],
    })
    const patchSpy = vi.spyOn(api, 'patchMeetingActionItem').mockResolvedValue({
      id: 'act_9',
      meeting_id: 'mtg_9',
      meeting_title: '周例会',
      text: '核对季度预算表',
      owner: '李四',
      due_date: '2026-11-27',
      priority: 'high',
      status: 'done',
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/inbox']}>
          <MantineProvider>
            <ActionInboxView onOpenSession={() => {}} />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('核对季度预算表')).toBeTruthy()
    })
    fireEvent.click(screen.getByRole('tab', { name: /会议行动项/ }))
    fireEvent.click(screen.getByRole('button', { name: '标为完成' }))
    await waitFor(() => {
      expect(patchSpy).toHaveBeenCalledWith({
        meeting_id: 'mtg_9',
        item_id: 'act_9',
        status: 'done',
      })
    })
  })

  it('manages multiple AI providers (OpenAI / Gemini / Magpie) and fetches models in AiProvidersSettingsSection', async () => {
    vi.spyOn(api, 'getAiProviders').mockResolvedValue({
      providers: [],
      default_provider_id: '',
      default_model: '',
      default_reasoning: 'low',
      effective_configured: false,
    })
    const fetchModelsSpy = vi.spyOn(api, 'fetchAiProviderModels').mockResolvedValue({
      ok: true,
      models: ['guolian-llm-max', 'qwen-plus'],
      count: 2,
    })
    const saveProviderSpy = vi.spyOn(api, 'saveAiProvider').mockResolvedValue({
      provider: {
        id: 'prv_mp',
        name: '公司 Magpie 网关',
        protocol: 'openai',
        vendor: 'magpie',
        base_url: 'https://magpie.corp/v1',
        api_key_set: true,
        masked_key: 'sk-mp...9999',
        models: ['guolian-llm-max', 'qwen-plus'],
        enabled: true,
        management_url: 'https://magpie.corp',
      },
      config: {
        providers: [
          {
            id: 'prv_mp',
            name: '公司 Magpie 网关',
            protocol: 'openai',
            vendor: 'magpie',
            base_url: 'https://magpie.corp/v1',
            api_key_set: true,
            masked_key: 'sk-mp...9999',
            models: ['guolian-llm-max', 'qwen-plus'],
            enabled: true,
            management_url: 'https://magpie.corp',
          },
        ],
        default_provider_id: 'prv_mp',
        default_model: 'guolian-llm-max',
        default_reasoning: 'low',
        effective_configured: true,
        effective_model: 'guolian-llm-max',
        effective_provider_name: '公司 Magpie 网关',
      },
    })

    render(
      <MantineProvider>
        <AiProvidersSettingsSection />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '添加供应商' })).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '添加供应商' }))
    fireEvent.click(screen.getByRole('button', { name: 'Magpie 网关' }))
    fireEvent.change(screen.getByLabelText('供应商名称'), { target: { value: '公司 Magpie 网关' } })
    fireEvent.change(screen.getByLabelText('供应商接口地址'), { target: { value: 'https://magpie.corp/v1' } })
    fireEvent.change(screen.getByLabelText('供应商 API Key'), { target: { value: 'sk-mp-secret-9999' } })
    fireEvent.change(screen.getByLabelText('网关管理地址'), { target: { value: 'https://magpie.corp' } })

    fireEvent.click(screen.getByRole('button', { name: '拉取模型' }))
    await waitFor(() => {
      expect(fetchModelsSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          protocol: 'openai',
          base_url: 'https://magpie.corp/v1',
          api_key: 'sk-mp-secret-9999',
        }),
      )
      expect(screen.getByText('guolian-llm-max')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '保存供应商' }))
    await waitFor(() => {
      expect(saveProviderSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          name: '公司 Magpie 网关',
          protocol: 'openai',
          vendor: 'magpie',
          base_url: 'https://magpie.corp/v1',
          management_url: 'https://magpie.corp',
          models: ['guolian-llm-max', 'qwen-plus'],
        }),
      )
      expect(screen.getByText('公司 Magpie 网关')).toBeTruthy()
    })
  })
})
