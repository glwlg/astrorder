import { MantineProvider } from '@mantine/core'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MarkdownContent } from './MarkdownContent'

describe('safe markdown rendering', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('does not mount raw HTML from an untrusted message', () => {
    const { container } = render(
      <MantineProvider><MarkdownContent value={'安全文本\n\n<script>alert("x")</script>'} /></MantineProvider>,
    )

    expect(screen.getByText('安全文本')).toBeInTheDocument()
    expect(container.querySelector('script')).toBeNull()
  })

  it('rejects executable and protocol-relative link targets', () => {
    const { container } = render(
      <MantineProvider><MarkdownContent value={'[脚本](javascript:alert(1)) [外站](//evil.example/a) [本地](/api/v1/attachments/a)'} /></MantineProvider>,
    )

    expect(container.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(container.querySelector('a[href^="//"]')).toBeNull()
    expect(container.querySelector('a[href="/api/v1/attachments/a"]')).not.toBeNull()
  })

  it('renders local file links and image preview buttons', () => {
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={'[方案](<C:/Users/test/file.drawio>) [预览](<C:/Users/test/pic.png>)'} />
      </MantineProvider>,
    )
    expect(container.querySelector('.markdown-file-link')).not.toBeNull()
    expect(container.querySelector('.markdown-image-link')).not.toBeNull()
  })

  it('renders markdown code blocks with language header and copy button', () => {
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={'```bash\necho hello\n```'} />
      </MantineProvider>,
    )
    expect(container.querySelector('.markdown-code-wrapper')).not.toBeNull()
    expect(screen.getByText('BASH')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '复制代码' })).toBeInTheDocument()
    expect(screen.getByText('echo hello')).toBeInTheDocument()
  })

  it('does not hijack shell commands inside inline backticks as file links', () => {
    render(
      <MantineProvider>
        <MarkdownContent value={'执行 `bash backend/scripts/check.sh` 与 `uv run scripts/check.py` 后通过'} />
      </MantineProvider>,
    )
    expect(screen.queryByRole('button', { name: /check\.sh/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /check\.py/ })).toBeNull()
    expect(screen.getByText('bash backend/scripts/check.sh')).toBeInTheDocument()
  })

  it('preserves file:// line anchors without corrupting links and omits redundant [代码] badges', () => {
    render(
      <MantineProvider>
        <MarkdownContent value={'查看 [httpapi.go](file:///home/user/app/httpapi.go#L331-L336) 和 [HANDOFF.md](file:///home/user/app/HANDOFF.md)'} />
      </MantineProvider>,
    )
    const goBtn = screen.getByRole('button', { name: /httpapi\.go/ })
    expect(goBtn).toHaveClass('markdown-file-link')
    expect(goBtn.textContent).toContain(':331-336')
    expect(goBtn.textContent).not.toContain('[代码]')
    const mdBtn = screen.getByRole('button', { name: /HANDOFF\.md/ })
    expect(mdBtn).toHaveClass('is-tone-doc')
    expect(mdBtn.textContent).not.toContain('[代码]')
  })

  it('renders GFM alert blockquotes as semantic callouts and strips the raw tag', () => {
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={'> [!TIP]\n> 记得点击托盘更新小内核'} />
      </MantineProvider>,
    )
    const callout = container.querySelector('.markdown-callout.is-tip')
    expect(callout).not.toBeNull()
    expect(screen.getByText('提示')).toBeInTheDocument()
    expect(screen.getByText('记得点击托盘更新小内核')).toBeInTheDocument()
    expect(screen.queryByText(/\[!TIP\]/)).toBeNull()
  })

  it('syntax highlights code blocks while keeping wrap toggle and intact text', () => {
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={'```go\nfunc hello() string {\n  return "ok"\n}\n```'} />
      </MantineProvider>,
    )
    expect(container.querySelector('.tok-keyword')?.textContent).toBe('func')
    expect(container.querySelector('.tok-string')?.textContent).toBe('"ok"')
    expect(screen.getByRole('button', { name: '开启自动换行' })).toBeInTheDocument()
  })

  it('skips per-token span explosion on very long code blocks while preserving intact text', () => {
    const longGo = Array.from({ length: 180 }, (_, i) => `func f${i}() string { return "v${i}" }`).join('\n')
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={`\`\`\`go\n${longGo}\n\`\`\``} />
      </MantineProvider>,
    )
    expect(container.querySelectorAll('.markdown-code-block span')).toHaveLength(0)
    expect(container.querySelector('.markdown-code-block code')?.textContent).toContain('func f179()')
  })

  it('shows hover file preview with highlighted target lines after hover intent delay', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('package main\n\nfunc main() {\n\tprintln("hi")\n}\n', { status: 200 }),
    )
    vi.stubGlobal('fetch', fetchMock)

    render(
      <MantineProvider>
        <MarkdownContent value={'查看 [main.go](file:///home/user/app/main.go#L3-L4)'} />
      </MantineProvider>,
    )

    const link = screen.getByRole('button', { name: /main\.go/ })
    fireEvent.mouseEnter(link)
    expect(screen.queryByRole('dialog', { name: /文件预览/ })).toBeNull()

    await act(async () => {
      vi.advanceTimersByTime(350)
      await Promise.resolve()
      await Promise.resolve()
    })

    const dialog = screen.getByRole('dialog', { name: '文件预览：main.go' })
    expect(dialog).toBeInTheDocument()
    expect(dialog.textContent).toContain('L3-4')
    expect(dialog.querySelectorAll('.file-hover-code-row.is-target')).toHaveLength(2)

    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('flips hover preview upward when viewport space below the link is insufficient', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('line 1\nline 2\n', { status: 200 })))
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 900 })

    render(
      <MantineProvider>
        <MarkdownContent value={'查看 [bottom.go](file:///home/user/app/bottom.go)'} />
      </MantineProvider>,
    )

    const link = screen.getByRole('button', { name: /bottom\.go/ })
    vi.spyOn(link, 'getBoundingClientRect').mockReturnValue({
      left: 200, right: 320, top: 640, bottom: 660, width: 120, height: 20, x: 200, y: 640, toJSON: () => ({}),
    })
    fireEvent.mouseEnter(link)
    await act(async () => {
      vi.advanceTimersByTime(350)
      await Promise.resolve()
      await Promise.resolve()
    })

    const dialog = screen.getByRole('dialog', { name: '文件预览：bottom.go' })
    expect(dialog).toHaveClass('is-above')
    expect(dialog.style.bottom).toBe('268px')
    expect(dialog.style.top).toBe('')
  })
})
