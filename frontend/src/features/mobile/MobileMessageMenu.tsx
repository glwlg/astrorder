import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react'
import { createPortal } from 'react-dom'
import { IconCopy, IconQuote, IconTextSize } from '@tabler/icons-react'
import type { MessageActionAnchor } from './MobileTranscript'
import './mobileMessageMenu.css'

export function MobileMessageMenu({ anchor, onClose, onCopy, onQuote }: { anchor: MessageActionAnchor; onClose: () => void; onCopy: () => void; onQuote: () => void }) {
  const menu = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ left: number; top: number; arrow: number; below: boolean } | null>(null)
  useLayoutEffect(() => {
    const update = () => {
      const viewport = window.visualViewport
      const leftEdge = (viewport?.offsetLeft || 0) + 12
      const topEdge = (viewport?.offsetTop || 0) + 12
      const width = viewport?.width || window.innerWidth
      const height = menu.current?.offsetHeight || 58
      const menuWidth = menu.current?.offsetWidth || 228
      const left = Math.max(leftEdge, Math.min(anchor.x - menuWidth / 2, leftEdge + width - menuWidth - 24))
      const below = anchor.y - height - 14 < topEdge
      const top = below ? anchor.y + 14 : anchor.y - height - 14
      setPosition({ left, top: Math.max(topEdge, Math.min(top, topEdge + (viewport?.height || window.innerHeight) - height - 24)), arrow: Math.max(14, Math.min(menuWidth - 14, anchor.x - left)), below })
    }
    update()
    window.addEventListener('resize', update)
    window.visualViewport?.addEventListener('resize', update)
    return () => { window.removeEventListener('resize', update); window.visualViewport?.removeEventListener('resize', update) }
  }, [anchor])
  useEffect(() => {
    const outside = (event: PointerEvent) => { if (!menu.current?.contains(event.target as Node)) onClose() }
    const scroll = () => onClose()
    document.addEventListener('pointerdown', outside, true)
    document.addEventListener('scroll', scroll, true)
    menu.current?.querySelector<HTMLButtonElement>('button')?.focus({ preventScroll: true })
    return () => { document.removeEventListener('pointerdown', outside, true); document.removeEventListener('scroll', scroll, true) }
  }, [onClose])
  const select = () => {
    const bubble = anchor.element.querySelector('.m-bubble')
    if (bubble) { const range = document.createRange(); range.selectNodeContents(bubble); const selection = window.getSelection(); selection?.removeAllRanges(); selection?.addRange(range) }
    onClose()
  }
  return createPortal(<div ref={menu} role="menu" aria-label="消息操作" className="m-message-popover" data-placement={position?.below ? 'below' : 'above'} style={{ left: position?.left, top: position?.top, visibility: position ? 'visible' : 'hidden', '--menu-arrow-x': `${position?.arrow || 20}px` } as CSSProperties}
    onKeyDown={event => {
      if (event.key === 'Escape') { event.preventDefault(); onClose(); anchor.element.focus({ preventScroll: true }) }
      if (event.key === 'Tab') onClose()
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
        event.preventDefault()
        const buttons = Array.from(menu.current?.querySelectorAll('button') || [])
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
        buttons[(index + (event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1) + buttons.length) % buttons.length]?.focus()
      }
    }}>
    <button role="menuitem" onClick={() => { onCopy(); onClose() }}><IconCopy size={17} />复制</button>
    <button role="menuitem" onClick={select}><IconTextSize size={17} />选择</button>
    <button role="menuitem" onClick={() => { onQuote(); onClose() }}><IconQuote size={17} />引用</button>
  </div>, document.body)
}
