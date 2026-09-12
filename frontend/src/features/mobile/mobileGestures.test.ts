import { describe, expect, it } from 'vitest'
import { closesSessionDrawerFromSwipe, installGlobalNativeHoldBlocker, opensSessionDrawerFromEdge, sessionCardPose, sessionSwipeDirection, sessionSwipeGesture, suppressNativeHold } from './mobileGestures'

describe('mobile gesture arbitration', () => {
  it('opens the session drawer only from a mostly horizontal left-edge right swipe', () => {
    expect(opensSessionDrawerFromEdge(8, 320, 110, 327)).toBe(true)
    expect(opensSessionDrawerFromEdge(8, 320, 110, 430)).toBe(false)
    expect(opensSessionDrawerFromEdge(48, 320, 150, 324)).toBe(false)
    expect(opensSessionDrawerFromEdge(8, 320, 50, 324)).toBe(false)
    expect(sessionSwipeGesture(8, 320, 160, 390)).toBeNull()
    expect(sessionSwipeGesture(32, 300, 180, 360)).toBeNull()
  })

  it('requires an inner diagonal swipe for session navigation', () => {
    expect(sessionSwipeDirection(260, 300, 140, 360)).toBe(1)
    expect(sessionSwipeDirection(140, 360, 260, 300)).toBe(-1)
    expect(sessionSwipeDirection(260, 300, 140, 306)).toBeNull()
    expect(sessionSwipeDirection(260, 300, 220, 350)).toBeNull()
  })

  it('sends the old card with the finger and brings the new card from the opposite corner', () => {
    expect(sessionSwipeGesture(260, 300, 140, 390)).toEqual({ direction: 1, vertical: 1, cut: 'next-down' })
    expect(sessionCardPose('next-down', 'leave')).toMatchObject({ x: -188, y: 148 })
    expect(sessionCardPose('next-down', 'enter')).toMatchObject({ x: 188, y: -148 })
    expect(sessionSwipeGesture(140, 390, 260, 300)).toEqual({ direction: -1, vertical: -1, cut: 'previous-up' })
    expect(sessionCardPose('previous-up', 'leave')).toMatchObject({ x: 188, y: -148 })
    expect(sessionCardPose('previous-up', 'enter')).toMatchObject({ x: -188, y: 148 })
  })

  it('closes the drawer with a leftward swipe inside the sheet', () => {
    expect(closesSessionDrawerFromSwipe(280, 320, 150, 330)).toBe(true)
    expect(closesSessionDrawerFromSwipe(280, 320, 220, 470)).toBe(false)
    expect(closesSessionDrawerFromSwipe(280, 320, 250, 328)).toBe(false)
    expect(closesSessionDrawerFromSwipe(280, 120, 270, 420)).toBe(false)
  })

  it('blocks native long-press menus except inside text fields', () => {
    const button = document.createElement('button')
    document.body.append(button)
    const blocked = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    button.addEventListener('contextmenu', suppressNativeHold)
    button.dispatchEvent(blocked)
    expect(blocked.defaultPrevented).toBe(true)

    const field = document.createElement('textarea')
    document.body.append(field)
    const allowed = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    field.addEventListener('contextmenu', suppressNativeHold)
    field.dispatchEvent(allowed)
    expect(allowed.defaultPrevented).toBe(false)
  })

  it('global blocker only cancels touch-triggered long-press, not desktop right-click', () => {
    const target = document.createElement('div')
    document.body.append(target)
    const detach = installGlobalNativeHoldBlocker()
    try {
      target.dispatchEvent(new TouchEvent('touchstart', { bubbles: true, cancelable: true, touches: [{ clientX: 10, clientY: 10, identifier: 1, target }] as never }))
      const hold = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
      target.dispatchEvent(hold)
      expect(hold.defaultPrevented).toBe(true)

      target.dispatchEvent(new TouchEvent('touchend', { bubbles: true, cancelable: true }))
      const click = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
      target.dispatchEvent(click)
      expect(click.defaultPrevented).toBe(false)

      const field = document.createElement('textarea')
      document.body.append(field)
      field.dispatchEvent(new TouchEvent('touchstart', { bubbles: true, cancelable: true, touches: [{ clientX: 2, clientY: 2, identifier: 2, target: field }] as never }))
      const editMenu = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
      field.dispatchEvent(editMenu)
      expect(editMenu.defaultPrevented).toBe(false)
    } finally {
      detach()
    }
  })
})
