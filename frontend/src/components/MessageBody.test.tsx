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
it('does not reinterpret quoted assistant text or code fences as attachment references', () => {
 const value = '```\n@image:C:\\private\\upload.png\n```'
 render(<MessageBody value={value} user renderMarkdown={text => <pre>{text}</pre>} />)
 expect(screen.queryByText('本地图片 · 尚无可用预览')).toBeNull()
 expect(screen.getByText(/@image:/)).toBeInTheDocument()
})
