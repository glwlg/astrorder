import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { MessageBody } from './MessageBody'
afterEach(cleanup)
it('presents native local image references without fetching arbitrary local files', () => {
 const text = '请查看\n@image:C:\\private\\upload.png'
 const { container } = render(<MessageBody value={text} user renderMarkdown={value => <p>{value}</p>} />)
 expect(screen.getByText('请查看')).toBeInTheDocument()
 expect(screen.getByText('upload.png')).toBeInTheDocument()
 expect(screen.getByText('本地图片 · 尚无可用预览')).toBeInTheDocument()
 expect(container.querySelector('img, a')).toBeNull()
 fireEvent.click(screen.getByText('查看原始引用'))
 expect(screen.getByText('@image:C:\\private\\upload.png')).toBeInTheDocument()
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
