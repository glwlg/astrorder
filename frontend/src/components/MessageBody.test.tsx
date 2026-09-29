import { MantineProvider } from '@mantine/core'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MessageBody } from './MessageBody'
afterEach(cleanup)
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
 const text = '请查看\n@image:/home/luwei/.hermes/images/upload.png'
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
 expect(img.src).toContain('path=%2Fhome%2Fluwei%2F.hermes%2Fimages%2Fupload.png')
})
