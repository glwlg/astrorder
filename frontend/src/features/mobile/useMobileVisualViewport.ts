import { useEffect, type RefObject } from 'react'

const KEYBOARD_SHRINK_THRESHOLD = 120
const FOCUSED_SHRINK_THRESHOLD = 60

function isEditingInside(root: HTMLElement): boolean {
  const active = document.activeElement
  if (!(active instanceof HTMLElement)) return false
  if (!root.contains(active)) return false
  return active.tagName === 'TEXTAREA' || active.tagName === 'INPUT' || active.isContentEditable
}

/**
 * Keeps the fixed mobile workspace aligned to window.visualViewport when the iOS/Android
 * virtual keyboard opens, preventing fixed composers from colliding with Safari's keyboard
 * accessory bar or scrolling the top header off-screen on first focus.
 */
export function useMobileVisualViewport(rootRef: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return

    let rafId: number | null = null
    let timers: number[] = []
    let wasKeyboardOpen = false

    const clearTimers = () => {
      if (rafId !== null) {
        window.cancelAnimationFrame(rafId)
        rafId = null
      }
      for (const timer of timers) window.clearTimeout(timer)
      timers = []
    }

    const sync = () => {
      const root = rootRef.current
      if (!root) return
      const vvHeight = Math.round(vv.height)
      const vvTop = Math.max(0, Math.round(vv.offsetTop))
      const shrink = Math.round(window.innerHeight - vv.height)
      const editing = isEditingInside(root)
      const keyboardOpen = vvHeight > 0 && (shrink > KEYBOARD_SHRINK_THRESHOLD || (editing && shrink > FOCUSED_SHRINK_THRESHOLD))

      if (keyboardOpen) {
        const scrollEl = !wasKeyboardOpen
          ? root.querySelector<HTMLElement>('.m-transcript, .bot-group-chat-messages')
          : null
        const nearBottom = scrollEl
          ? scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight < 160
          : false

        root.style.setProperty('--m-vv-height', `${vvHeight}px`)
        root.style.setProperty('--m-vv-top', `${vvTop}px`)
        root.dataset.keyboardOpen = 'true'

        if (window.scrollY !== 0) {
          window.scrollTo(0, 0)
        }
        if (!wasKeyboardOpen) {
          wasKeyboardOpen = true
          if (scrollEl && nearBottom) {
            window.requestAnimationFrame(() => {
              scrollEl.scrollTop = scrollEl.scrollHeight
            })
          }
        }
      } else if (wasKeyboardOpen || root.dataset.keyboardOpen) {
        wasKeyboardOpen = false
        root.style.removeProperty('--m-vv-height')
        root.style.removeProperty('--m-vv-top')
        delete root.dataset.keyboardOpen
        if (window.scrollY !== 0) {
          window.scrollTo(0, 0)
        }
      }
    }

    const scheduleSync = () => {
      if (rafId !== null) window.cancelAnimationFrame(rafId)
      rafId = window.requestAnimationFrame(() => {
        rafId = null
        sync()
      })
    }

    const scheduleSettledSync = () => {
      sync()
      scheduleSync()
      for (const timer of timers) window.clearTimeout(timer)
      timers = [80, 220, 400].map(delay => window.setTimeout(sync, delay))
    }

    vv.addEventListener('resize', scheduleSync)
    vv.addEventListener('scroll', scheduleSync)
    window.addEventListener('resize', scheduleSync)
    window.addEventListener('orientationchange', scheduleSettledSync)
    document.addEventListener('focusin', scheduleSettledSync)
    document.addEventListener('focusout', scheduleSettledSync)

    sync()

    return () => {
      clearTimers()
      vv.removeEventListener('resize', scheduleSync)
      vv.removeEventListener('scroll', scheduleSync)
      window.removeEventListener('resize', scheduleSync)
      window.removeEventListener('orientationchange', scheduleSettledSync)
      document.removeEventListener('focusin', scheduleSettledSync)
      document.removeEventListener('focusout', scheduleSettledSync)
      const root = rootRef.current
      if (root) {
        root.style.removeProperty('--m-vv-height')
        root.style.removeProperty('--m-vv-top')
        delete root.dataset.keyboardOpen
      }
    }
  }, [rootRef])
}
