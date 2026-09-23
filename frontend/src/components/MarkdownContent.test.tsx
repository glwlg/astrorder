import { MantineProvider } from '@mantine/core'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MarkdownContent } from './MarkdownContent'

describe('safe markdown rendering', () => {
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
})
