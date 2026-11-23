import { createContext, useContext, useEffect, useId, useRef, type ReactNode } from 'react'
import { useComputedColorScheme } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'
import type { LiquidGlassLens, LiquidGlassOptions } from 'liquid-gl'
import { createGlassRim } from './glassRim'
import { loadLiquidGlass } from './liquidGlass'
import './glassPanel.css'

export type GlassEngine = 'css' | 'webgl'
type ComposerGlassScope = { snapshot: string; engine: GlassEngine }
const ComposerGlassContext = createContext<ComposerGlassScope | null>(null)
type GlassVariant = 'panel' | 'composer' | 'menu'
const materials = {
  panel: { refraction: 0, bevelDepth: 0.009, bevelWidth: 0.09 },
  composer: { refraction: 0, bevelDepth: 0.004, bevelWidth: 0.08 },
  menu: { refraction: 0, bevelDepth: 0.006, bevelWidth: 0.08 },
}
const instances = (result: LiquidGlassLens | LiquidGlassLens[] | undefined) =>
  Array.isArray(result) ? result : result ? [result] : []

export function ComposerGlassSurface({
  active,
  children,
  snapshot = '.chat-workbench',
  engine = 'css',
}: {
  active: boolean
  children: ReactNode
  snapshot?: string
  engine?: GlassEngine
}) {
  return active ? <GlassPanel active snapshot={snapshot} variant="composer" engine={engine}>{children}</GlassPanel> : children
}

/** React context also reaches Mantine's portalled dropdowns. */
export function ComposerGlassMenu({ active, children }: { active: boolean; children: ReactNode }) {
  const scope = useContext(ComposerGlassContext)
  return scope ? <GlassPanel active={active} snapshot={scope.snapshot} variant="menu" engine={scope.engine}>{children}</GlassPanel> : children
}

export function GlassButtonLens() {
  const scope = useContext(ComposerGlassContext)
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  return scope?.engine === 'webgl' ? <span id={`glass-button-${id}`} className="glass-button-lens" aria-hidden="true" /> : null
}

/** Frosted floating panel; defaults to pure GPU CSS backdrop-filter without html2canvas snapshots. */
export function GlassPanel({ children, active, snapshot, variant = 'panel', engine = 'css' }: {
  children: ReactNode; active: boolean; snapshot: string; variant?: GlassVariant; engine?: GlassEngine
}) {
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const surfaceRef = useRef<HTMLDivElement>(null)
  const scheme = useComputedColorScheme('light')
  const reducedMotion = useReducedMotion()

  useEffect(() => {
    const surface = surfaceRef.current
    if (!surface) return
    if (!active) {
      delete surface.dataset.materialStatus
      return
    }
    if (engine === 'css') {
      surface.dataset.materialStatus = 'ready'
      return () => {
        delete surface.dataset.materialStatus
      }
    }
    let cancelled = false
    let lenses: LiquidGlassLens[] = []
    const buttons = new Map<HTMLElement, LiquidGlassLens[]>()
    const rims = new Map<HTMLCanvasElement, ReturnType<typeof createGlassRim>>()
    let observer: MutationObserver | undefined
    let buttonObserver: MutationObserver | undefined
    let refreshTimer: ReturnType<typeof setTimeout> | undefined
    const backdrop = document.querySelector(snapshot)!
    const trackRim = (target: HTMLElement, bound: LiquidGlassLens[], width: number) => {
      for (const lens of bound) {
        if (!lens.renderer) continue
        const canvas = lens.renderer.canvas
        if (!rims.has(canvas)) rims.set(canvas, createGlassRim(canvas))
        rims.get(canvas)!.track(target, width)
      }
    }
    const updateRims = () => { for (const rim of rims.values()) rim.update() }
    // liquidGL 3.0.0 only watches document scrolling. Chat messages scroll
    // inside their own container, so explicitly refresh that snapshot.
    const refreshSnapshot = () => {
      updateRims()
      if (cancelled || refreshTimer !== undefined) return
      refreshTimer = setTimeout(() => {
        refreshTimer = undefined
        const all = [...lenses, ...[...buttons.values()].flat()]
        for (const renderer of new Set(all.map(lens => lens.renderer))) {
          if (renderer) void renderer.captureSnapshot()
        }
      }, 160)
    }
    surface.dataset.materialStatus = 'loading'

    void loadLiquidGlass().then(({ default: liquidGL }) => {
      if (cancelled) return
      const options: LiquidGlassOptions = {
        target: `#glass-lens-${id}`,
        snapshot,
        content: false,
        engine: 'auto',
        resolution: Math.min(window.devicePixelRatio, 1.5),
        zIndex: 3,
        ...materials[variant],
        aberration: 0,
        magnify: 1,
        frost: 0,
        tint: scheme === 'dark' ? 'rgba(100, 88, 116, 0.2)' : null,
        shadow: false,
        specular: !reducedMotion,
        reveal: 'none',
        tilt: false,
        on: { init: () => { if (!cancelled) surface.dataset.materialStatus = 'ready' } },
      }
      lenses = instances(liquidGL(options))
      trackRim(surface.querySelector<HTMLElement>('.glass-panel-lens')!, lenses, variant === 'panel' ? 6 : 5)
      if (variant === 'composer') {
        // Register the body first, then its buttons. All use the same sticky
        // anchor and z-index, so liquidGL shares one canvas and draws in order.
        const syncButtons = () => {
          if (cancelled) return
          for (const [element, bound] of buttons) {
            if (!surface.contains(element)) {
              for (const rim of rims.values()) rim.untrack(element)
              for (const lens of bound) lens.destroy()
              buttons.delete(element)
            }
          }
          for (const element of surface.querySelectorAll<HTMLElement>('.glass-button-lens')) {
            if (buttons.has(element)) continue
            const bound = instances(liquidGL({
              ...options, target: `#${element.id}`,
              refraction: 0, bevelDepth: 0.002, bevelWidth: 0.16,
              specular: false,
            }))
            buttons.set(element, bound)
            trackRim(element, bound, 2)
          }
          updateRims()
        }
        syncButtons()
        buttonObserver = new MutationObserver(syncButtons)
        buttonObserver.observe(surface, { childList: true, subtree: true })
      }
      observer = new MutationObserver(records => {
        if (records.some(record => {
          const element = record.target instanceof Element ? record.target : record.target.parentElement
          return !element?.closest('[data-liquid-ignore]')
        })) refreshSnapshot()
      })
      observer.observe(backdrop, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['class', 'style'] })
      backdrop.addEventListener('scroll', refreshSnapshot, true)
      surface.addEventListener('scroll', updateRims, true)
    }).catch((error: unknown) => {
      if (cancelled) return
      surface.dataset.materialStatus = 'error'
      console.error('工作台玻璃效果初始化失败', error)
    })

    return () => {
      cancelled = true
      observer?.disconnect()
      buttonObserver?.disconnect()
      backdrop.removeEventListener('scroll', refreshSnapshot, true)
      surface.removeEventListener('scroll', updateRims, true)
      clearTimeout(refreshTimer)
      for (const rim of rims.values()) rim.destroy()
      for (const lens of lenses) lens.destroy()
      for (const bound of buttons.values()) for (const lens of bound) lens.destroy()
      delete surface.dataset.materialStatus
    }
  }, [active, engine, id, reducedMotion, scheme, snapshot, variant])

  const scopeValue = variant === 'composer' ? { snapshot, engine } : null

  return (
    <div ref={surfaceRef} className={`glass-panel glass-panel--${variant}${active ? ' is-active' : ''}`} data-liquid-ignore>
      {engine === 'webgl' && <div id={`glass-lens-${id}`} className="glass-panel-lens" aria-hidden="true" />}
      <div className="glass-panel-content" data-liquid-ignore>
        {scopeValue ? <ComposerGlassContext.Provider value={scopeValue}>{children}</ComposerGlassContext.Provider> : children}
      </div>
    </div>
  )
}
