import { Drawer, type DrawerProps } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

const DrawerScope = createContext<((delta: number) => void) | null>(null)

export function SettingsDrawerScope({ children }: { children: (hasOpenDrawer: boolean) => ReactNode }) {
  const [openCount, setOpenCount] = useState(0)
  const changeCount = useCallback((delta: number) => setOpenCount(count => count + delta), [])
  return <DrawerScope.Provider value={changeCount}>{children(openCount > 0)}</DrawerScope.Provider>
}

/** Keep nested settings dialogs in their owning modal, including the overlay. */
export function SettingsDrawer({ children, ...props }: DrawerProps) {
  const [target, setTarget] = useState<HTMLElement | null>(null)
  const reducedMotion = useReducedMotion()
  const changeCount = useContext(DrawerScope)
  useEffect(() => {
    if (!props.opened || !changeCount) return
    changeCount(1)
    return () => changeCount(-1)
  }, [props.opened, changeCount])
  const attach = useCallback((node: HTMLSpanElement | null) => {
    if (node) setTarget(node.closest<HTMLElement>('.mantine-Modal-content') ?? node.parentElement)
  }, [])

  return <>
    <span ref={attach} hidden />
    {target && <Drawer
      {...props}
      className="settings-contained-drawer"
      withinPortal
      portalProps={{ target }}
      position="right"
      size="min(480px, 100%)"
      lockScroll={false}
      withOverlay
      trapFocus
      returnFocus
      closeOnEscape
      closeOnClickOutside
      transitionProps={{ transition: 'slide-left', duration: reducedMotion ? 0 : 260, timingFunction: 'cubic-bezier(0.22, 1, 0.36, 1)' }}
      overlayProps={{ color: '#141b26', backgroundOpacity: 0.28, style: { position: 'absolute', inset: 0, pointerEvents: 'auto' } }}
      styles={{
        root: { position: 'absolute', inset: 0, zIndex: 2000, pointerEvents: 'none' },
        inner: { position: 'absolute', inset: 0, width: '100%', height: '100%' },
        content: { display: 'flex', flexDirection: 'column', overflow: 'hidden', borderLeft: '1px solid var(--astr-border)' },
        header: { flexShrink: 0, borderBottom: '1px solid var(--astr-border)' },
        body: { flex: '1 1 0', minHeight: 0, overflowY: 'auto', padding: '20px 24px', overscrollBehavior: 'contain' },
      }}
    >{children}</Drawer>}
  </>
}
