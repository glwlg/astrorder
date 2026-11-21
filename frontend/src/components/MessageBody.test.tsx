import { MantineProvider } from '@mantine/core'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MessageBody } from './MessageBody'
afterEach(cleanup)
it('defers parsing a huge user message until expanded', () => {
 const text = `开始\n${'很长的内容 **Markdown**\n'.repeat(20000)}最后一行`
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 const view = render(<MessageBody value={text} user renderMarkdown={markdown} sessionId="s" />)
 expect(markdown).not.toHaveBeenCalled()
 expect(view.container.textContent!.length).toBeLessThan(800)
 expect(view.container.querySelector('.message-long-preview')!.textContent).toMatch(/^开始\n/)
 expect(screen.queryByText(/最后一行/)).toBeNull()
 const summary = screen.getByText('展开全文')
 fireEvent.click(summary)
 expect(markdown).toHaveBeenCalledWith(text)
 expect(view.container.querySelector('details')).toHaveAttribute('open')
 expect(view.container.textContent).toContain('最后一行')
 fireEvent.click(screen.getByText('收起长消息'))
 expect(view.container.querySelector('details')).not.toHaveAttribute('open')
 expect(view.container.textContent).not.toContain('最后一行')
})
it('folds short but tall messages and leaves ordinary multiline text intact', () => {
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 const view = render(<MessageBody value={'行\n'.repeat(40) + '末尾'} user renderMarkdown={markdown} />)
 expect(markdown).not.toHaveBeenCalled()
 expect(view.container.querySelector('.message-long-preview')!.textContent!.split('\n')).toHaveLength(8)
 expect(screen.getByText('展开全文')).toBeInTheDocument()
 view.rerender(<MessageBody value={'行\n'.repeat(39) + '末尾'} user renderMarkdown={markdown} />)
 expect(markdown).toHaveBeenCalledWith('行\n'.repeat(39) + '末尾')
 expect(screen.queryByText('展开全文')).toBeNull()
})
it('folds a single long line only beyond the size limit', () => {
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 const view = render(<MessageBody value={'字'.repeat(4000)} user renderMarkdown={markdown} />)
 expect(markdown).toHaveBeenCalledTimes(1)
 markdown.mockClear()
 view.rerender(<MessageBody value={'字'.repeat(4001)} user renderMarkdown={markdown} />)
 expect(markdown).not.toHaveBeenCalled()
 expect(view.container.querySelector('.message-long-preview')!.textContent).toHaveLength(601)
})
it('keeps long Agent replies fully visible as they stream', () => {
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 const view = render(<MessageBody value={'字'.repeat(3999)} renderMarkdown={markdown} />)
 const text = `${'很长的回复\n'.repeat(1000)}最后一行`
 view.rerender(<MessageBody value={text} renderMarkdown={markdown} />)
 expect(markdown).toHaveBeenLastCalledWith(text)
 expect(view.container.textContent).toContain('最后一行')
 expect(view.container.querySelector('.message-long')).toBeNull()
 expect(screen.queryByText('展开全文')).toBeNull()
})
it('preserves expanded user text during updates and resets when changing sessions', () => {
 const text = '正文\n'.repeat(1500)
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 const view = render(<MessageBody value={text} user renderMarkdown={markdown} sessionId="s1" />)
 fireEvent.click(screen.getByText('展开全文'))
 view.rerender(<MessageBody value={text + '追加内容'} user renderMarkdown={markdown} sessionId="s1" />)
 expect(view.container.textContent).toContain('追加内容')
 expect(screen.getByText('收起长消息')).toBeInTheDocument()
 markdown.mockClear()
 view.rerender(<MessageBody value={text} user renderMarkdown={markdown} sessionId="s2" />)
 expect(markdown).not.toHaveBeenCalled()
 expect(screen.getByText('展开全文')).toBeInTheDocument()
})
it('keeps native compaction notices in their existing disclosure even when huge', () => {
 const text = '[CONTEXT COMPACTION — REFERENCE ONLY]' + '摘要'.repeat(4000)
 render(<MessageBody value={text} renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.getByText('上下文压缩摘要')).toBeInTheDocument()
 expect(screen.queryByText('展开全文')).toBeNull()
 expect(screen.queryByText(text)).toBeNull()
})
it('renders native Hermes bracketed image markers with session routing', () => {
 const text = '请查看\n[Image attached at: C:\\private\\upload.png] [screenshot]'
 const markdown = vi.fn((value: string) => <p>{value}</p>)
 render(<MessageBody value={text} user sessionId="s" renderMarkdown={markdown} />)
 expect(screen.getByAltText('upload.png')).toHaveAttribute('src', '/api/v1/files/raw?path=C%3A%5Cprivate%5Cupload.png&session_id=s')
 expect(markdown).toHaveBeenCalledWith('请查看')
})
it('folds native compaction summaries and model notices without losing their text', () => {
 const text = '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into a summary.'
 render(<MessageBody value={text} user renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.queryByText(text)).toBeNull()
 fireEvent.click(screen.getByText('上下文压缩摘要'))
 expect(screen.getByText(text)).toBeInTheDocument()
})
it('announces live compaction progress and completion', () => {
 const { rerender } = render(<MessageBody value="正在压缩上下文" renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.getByRole('status')).toHaveTextContent('正在压缩上下文，请稍候…')
 rerender(<MessageBody value="上下文压缩完成" renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.getByRole('status')).toHaveTextContent('上下文压缩完成')
})
it('shows an @image reference exactly once, in the attachment area, and strips the raw path from the body', () => {
 const text = '请查看\n@image:C:\\private\\upload.png'
 const renderMarkdown = vi.fn((value: string) => <p>{value}</p>)
 render(<MessageBody value={text} user onImageClick={vi.fn()} renderMarkdown={renderMarkdown} />)
 // Exactly one image, in the attachment area
 expect(screen.getAllByAltText('upload.png')).toHaveLength(1)
 // The raw @image path must NOT be forwarded to the markdown body (would re-render as a second image/link)
 const bodyArg = renderMarkdown.mock.calls[0][0]
 expect(bodyArg).not.toContain('@image:')
 expect(bodyArg).not.toContain('upload.png')
 expect(bodyArg.trim()).toBe('请查看')
})
it('localizes only exact native interruption notices, preserving raw evidence', () => {
 render(<MessageBody value="Operation interrupted." renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.getByText('本轮已中断')).toBeInTheDocument()
 expect(screen.queryByText('运行失败')).toBeNull()
 fireEvent.click(screen.getByText('查看原始记录'))
 expect(screen.getByText('Operation interrupted.')).toBeInTheDocument()
})
it('extracts and previews MEDIA: lines from assistant message text', () => {
 const text = '生成完成！\nMEDIA:P:\\AI\\Image\\outputs\\demo.png\n请查收。'
 const onImage = vi.fn()
 render(
   <MessageBody
     value={text}
     user={false}
     onImageClick={onImage}
     renderMarkdown={(v) => <p>{v}</p>}
   />,
 )
 const btn = screen.getByRole('button', { name: /demo\.png/ })
 expect(btn).toBeInTheDocument()
 expect(screen.queryByText(/MEDIA:/)).toBeNull()
})
it('renders sent user code comments as a review card while preserving surrounding text', () => {
 const first = String.raw`::code-comment{title="[P1] 离线依赖不兼容" body="修复基础镜像。" file="/repo/Dockerfile" start=25 priority=1}`
 const second = String.raw`::code-comment{title="[P2] 地址被覆盖" body="保留配置地址。" file="/repo/service.py" start=70 priority=2}`
 const renderMarkdown = vi.fn((value: string) => <p>{value}</p>)
 render(<MantineProvider><MessageBody value={`请处理以下问题：\n\n${first}\n\n${second}`} user renderMarkdown={renderMarkdown} /></MantineProvider>)
 expect(screen.getByText('2 comments')).toBeInTheDocument()
 expect(screen.getByText('离线依赖不兼容')).toBeInTheDocument()
 expect(screen.getByText('地址被覆盖')).toBeInTheDocument()
 expect(renderMarkdown).toHaveBeenCalledWith('请处理以下问题：')
 expect(screen.queryByText(/::code-comment/)).not.toBeInTheDocument()
})

it('renders a standalone code comment without repeating its raw directive', () => {
 const raw = String.raw`::code-comment{title="[P1] 缺少依据" body="模型返回 {\"status\":\"FIXED\"} 时不能直接关闭。" file="/repo/review.py" start=105 priority=1}`
 render(<MantineProvider><MessageBody value={raw} renderMarkdown={value => <p>{value}</p>} /></MantineProvider>)
 expect(screen.getByText('缺少依据')).toBeInTheDocument()
 expect(screen.queryByText(/::code-comment/)).not.toBeInTheDocument()
})
it('passes sessionId and connectionId to file raw url for @image references', () => {
 const text = '请查看\n@image:/home/fixture/.hermes/images/upload.png'
 render(
   <MessageBody
     value={text}
     user
     sessionId="session-123"
     connectionId="ssh-456"
     renderMarkdown={(val) => <p>{val}</p>}
   />,
 )
 const img = screen.getByAltText('upload.png') as HTMLImageElement
 expect(img.src).toContain('session_id=session-123')
 expect(img.src).toContain('connection_id=ssh-456')
 expect(img.src).toContain('path=%2Fhome%2Ffixture%2F.hermes%2Fimages%2Fupload.png')
})
