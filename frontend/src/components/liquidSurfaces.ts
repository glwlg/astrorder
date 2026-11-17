import type { LiquidGlassLens } from 'liquid-gl'
import { createGlassRim } from './glassRim'
import { loadLiquidGlass } from './liquidGlass'
import './liquidSurfaces.css'

const selector = [
  '[data-liquid-surface]',
  '.mantine-Menu-dropdown',
  '.mantine-Popover-dropdown',
  '.mantine-HoverCard-dropdown',
  '.mantine-Notification-root',
].join(',')
let nextId = 0

/** Decorate existing controls without replacing their DOM, focus or event handlers. */
export function installLiquidSurfaces({ root = document.body, snapshot = 'body', dark = false, reducedMotion = false }: {
  root?: HTMLElement; snapshot?: string; dark?: boolean; reducedMotion?: boolean
} = {}) {
  let disposed = false
  let scheduled = false
  let refreshTimer: ReturnType<typeof setTimeout> | undefined
  type Binding = {
    layer: HTMLElement
    lenses: LiquidGlassLens[]
    rims: ReturnType<typeof createGlassRim>[]
    ignored: boolean
  }
  const bindings = new Map<HTMLElement, Binding>()
  const release = (surface: HTMLElement, binding: Binding) => {
    for (const rim of binding.rims) rim.destroy()
    for (const lens of binding.lenses) lens.destroy()
    binding.layer.remove()
    surface.classList.remove('liquid-surface')
    delete surface.dataset.materialStatus
    if (!binding.ignored) surface.removeAttribute('data-liquid-ignore')
    bindings.delete(surface)
  }
  const refresh = () => {
    if (refreshTimer !== undefined || disposed) return
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined
      const renderers = new Set([...bindings.values()].flatMap(binding => binding.lenses.map(lens => lens.renderer)))
      for (const renderer of renderers) if (renderer) void renderer.captureSnapshot()
    }, 160)
  }
  const visible = (element: HTMLElement) => {
    const style = getComputedStyle(element)
    return element.getClientRects().length > 0 && style.visibility !== 'hidden' && style.display !== 'none'
  }
  const ready = loadLiquidGlass()
  const sync = () => {
    scheduled = false
    if (disposed) return
    for (const [surface, binding] of bindings) {
      if (!surface.isConnected || !surface.matches(selector) || !visible(surface) || !surface.contains(binding.layer)) release(surface, binding)
      else if (!surface.classList.contains('liquid-surface')) surface.classList.add('liquid-surface')
    }
    for (const surface of root.querySelectorAll<HTMLElement>(selector)) {
      // Composer menus already own a lens. Never glassify the same surface twice.
      if (bindings.has(surface) || (surface.closest('.glass-panel') && !surface.hasAttribute('data-liquid-surface')) || surface.querySelector(':scope > .glass-panel') || !visible(surface)) continue
      const layer = document.createElement('span')
      layer.className = 'liquid-surface-layer'
      layer.setAttribute('aria-hidden', 'true')
      layer.setAttribute('data-liquid-ignore', '')
      const lensTarget = document.createElement('span')
      lensTarget.className = 'liquid-surface-lens'
      lensTarget.id = `liquid-surface-${++nextId}`
      layer.append(lensTarget)
      const binding: Binding = { layer, lenses: [], rims: [], ignored: surface.hasAttribute('data-liquid-ignore') }
      bindings.set(surface, binding)
      surface.classList.add('liquid-surface')
      surface.setAttribute('data-liquid-ignore', '')
      surface.dataset.materialStatus = 'loading'
      surface.append(layer)
      void ready.then(({ default: liquidGL }) => {
        if (disposed || bindings.get(surface) !== binding) return
        const compact = surface.dataset.liquidSurface === 'control' || surface.dataset.liquidSurface === 'selection'
        const result = liquidGL({
          target: `#${lensTarget.id}`,
          snapshot: surface.dataset.liquidSnapshot || snapshot,
          content: false, engine: 'auto', resolution: Math.min(window.devicePixelRatio, 1.5),
          zIndex: 0, refraction: 0, bevelDepth: compact ? 0.002 : 0.006,
          bevelWidth: compact ? 0.16 : 0.08, aberration: 0, magnify: 1, frost: 0,
          tint: dark ? 'rgba(100, 88, 116, 0.2)' : null,
          shadow: false, specular: !compact && !reducedMotion, reveal: 'none', tilt: false,
          on: { init: () => { if (!disposed && bindings.get(surface) === binding) surface.dataset.materialStatus = 'ready' } },
        })
        binding.lenses = Array.isArray(result) ? result : result ? [result] : []
        for (const canvas of new Set(binding.lenses.flatMap(lens => lens.renderer ? [lens.renderer.canvas] : []))) {
          const rim = createGlassRim(canvas)
          rim.track(lensTarget, compact ? 2 : 5)
          binding.rims.push(rim)
        }
      }).catch((error: unknown) => {
        if (disposed || bindings.get(surface) !== binding) return
        surface.dataset.materialStatus = 'error'
        console.error('悬浮玻璃效果初始化失败', error)
      })
    }
  }
  // Menus can close while the window is in the background, where animation
  // frames are suspended. Cleanup must not wait for the window to become active.
  const schedule = () => {
    if (disposed || scheduled) return
    scheduled = true
    queueMicrotask(sync)
  }
  const observer = new MutationObserver(records => {
    let changed = false
    for (const record of records) {
      const element = record.target instanceof Element ? record.target : record.target.parentElement
      if (!element?.closest('[data-liquid-ignore]')) changed = true
      if (record.type === 'attributes' && element instanceof HTMLElement && (
        bindings.has(element) || element.matches(selector) || element.querySelector(selector)
      )) schedule()
      if (record.type === 'childList' && (
        [...record.removedNodes, ...record.addedNodes].some(node => node instanceof Element && (node.matches(selector) || node.querySelector(selector)))
      )) schedule()
    }
    if (changed) refresh()
  })
  observer.observe(root, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['class', 'style', 'hidden', 'data-liquid-surface'] })
  root.addEventListener('scroll', refresh, true)
  root.addEventListener('load', refresh, true)
  window.addEventListener('resize', schedule)
  sync()
  return () => {
    disposed = true
    observer.disconnect()
    clearTimeout(refreshTimer)
    root.removeEventListener('scroll', refresh, true)
    root.removeEventListener('load', refresh, true)
    window.removeEventListener('resize', schedule)
    for (const [surface, binding] of bindings) release(surface, binding)
  }
}
