import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { MobileTaskNotices } from './MobileTaskNotices'

it('dismisses a notice without opening or stopping the task and keeps new tasks visible', () => {
 const onOpen = vi.fn()
 const task = { id: 'one', title: '子代理消息' }
 const { unmount } = render(<MobileTaskNotices scope="session-a" tasks={[task]} onOpen={onOpen} />)
 const notice = screen.getByText(task.title)
 fireEvent.touchStart(notice, { touches: [{ clientX: 200, clientY: 30 }] })
 fireEvent.touchMove(notice, { touches: [{ clientX: 100, clientY: 32 }] })
 fireEvent.touchEnd(notice, { changedTouches: [{ clientX: 100, clientY: 32 }] })
 expect(screen.queryByText(task.title)).toBeNull()
 expect(onOpen).not.toHaveBeenCalled()
 unmount()
 render(<MobileTaskNotices scope="session-a" tasks={[{ ...task }, { id: 'two', title: '新消息' }]} onOpen={onOpen} />)
 expect(screen.queryByText(task.title)).toBeNull()
 fireEvent.click(screen.getByText('新消息'))
 expect(onOpen).toHaveBeenCalledWith({ id: 'two', title: '新消息' })
})
