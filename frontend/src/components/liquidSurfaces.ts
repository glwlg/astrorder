import type { LiquidGlassLens } from 'liquid-gl'
import { createGlassRim } from './glassRim'
import { loadLiquidGlass } from './liquidGlass'
import './liquidSurfaces.css'

const sidebar = '.workspace-sidebar[data-liquid-snapshot]'
const sidebarControls = [
  '.sidebar-nav-link[data-liquid-surface]',
  '.session-search-field',
  '.agent-connection-control',
  '.agent-kind-select',
  '.session-quick-filters',
  '.session-project-header',
].map(control => `${sidebar} ${control}`).join(',')
const selector = [
  '[data-liquid-surface]',
  sidebarControls,
  '.mantine-Menu-dropdown',
  '.mantine-Popover-dropdown',
  '.mantine-HoverCard-dropdown',
  '.mantine-Notification-root',
].join(',')
let nextId = 0

function isCssOnlyStaticBackdrop(snapshotSelector: string, sidebarHost: HTMLElement | null): boolean {
  if (sidebarHost) return false
  return (
    snapshotSelector === '#astr-shell-sky' ||
    snapshotSelector === '.welcome-ambient-backdrop' ||
    snapshotSelector === '.swarm-dag-backdrop' ||
    snapshotSelector === '.monitor-ambient-backdrop'
  )
}

function isStaticBackdropSnapshot(snapshotSelector: string): boolean {
  return (
    snapshotSelector === '#astr-shell-sky' ||
    snapshotSelector === '.welcome-ambient-backdrop' ||
    snapshotSelector === '.swarm-dag-backdrop' ||
    snapshotSelector === '.monitor-ambient-backdrop' ||
    snapshotSelector.startsWith('[id="')
  )
}

/** Decorate existing controls without replacing their DOM, focus or event handlers. */
export function installLiquidSurfaces({ root = document.body, snapshot = 'body', dark = false, reducedMotion = false, engine = 'webgl' }: {
  root?: HTMLElement; snapshot?: string; dark?: boolean; reducedMotion?: boolean; engine?: 'css' | 'webgl'
} = {}) {
  let disposed = false
  let scheduled = false
  let refreshTimer: ReturnType<typeof setTimeout> | undefined
  type Binding = {
    layer: HTMLElement
    lensTarget: HTMLElement
    lenses: LiquidGlassLens[]
    canvases: HTMLCanvasElement[]
    ignored: boolean
    positioned: boolean
    staticBackdrop: boolean
  }
  const bindings = new Map<HTMLElement, Binding>()
  const rims = new Map<HTMLCanvasElement, { mask: ReturnType<typeof createGlassRim>; targets: number }>()
  const release = (surface: HTMLElement, binding: Binding) => {
    for (const canvas of binding.canvases) {
      const rim = rims.get(canvas)!
      rim.mask.untrack(binding.lensTarget)
      if (--rim.targets === 0) {
        rim.mask.destroy()
        rims.delete(canvas)
      }
    }
    for (const lens of binding.lenses) lens.destroy()
    binding.layer.remove()
    surface.classList.remove('liquid-surface')
    delete surface.dataset.materialStatus
    if (!binding.ignored) surface.removeAttribute('data-liquid-ignore')
    if (binding.positioned) surface.style.removeProperty('position')
    bindings.delete(surface)
  }
  const refresh = () => {
    if (refreshTimer !== undefined || disposed) return
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined
      for (const { mask } of rims.values()) mask.update()
      // Skip redundant html2canvas re-captures for dedicated static ambient sky backdrops during streaming
      const renderers = new Set(
        [...bindings.values()]
          .filter(binding => !binding.staticBackdrop)
          .flatMap(binding => binding.lenses.map(lens => lens.renderer)),
      )
      for (const renderer of renderers) if (renderer) void renderer.captureSnapshot()
    }, 160)
  }
  const visible = (element: HTMLElement) => {
    const style = getComputedStyle(element)
    return element.getClientRects().length > 0 && style.visibility !== 'hidden' && style.display !== 'none'
  }
  const ready = engine === 'css' ? null : loadLiquidGlass()
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
      const sidebarHost = surface.closest<HTMLElement>(sidebar)
      const resolvedSnapshot = surface.dataset.liquidSnapshot || sidebarHost?.dataset.liquidSnapshot || snapshot
      const cssOnly =
        engine === 'css' ||
        surface.dataset.liquidEngine === 'css' ||
        isCssOnlyStaticBackdrop(resolvedSnapshot, sidebarHost)
      const needsPosition = getComputedStyle(surface).position === 'static'
      const binding: Binding = {
        layer,
        lensTarget,
        lenses: [],
        canvases: [],
        ignored: surface.hasAttribute('data-liquid-ignore'),
        positioned: needsPosition,
        staticBackdrop: cssOnly || isStaticBackdropSnapshot(resolvedSnapshot),
      }
      bindings.set(surface, binding)
      if (needsPosition) surface.style.position = 'relative'
      surface.classList.add('liquid-surface')
      surface.setAttribute('data-liquid-ignore', '')
      surface.append(layer)
      // Pure GPU-composited CSS Liquid Glass fast path: zero WebGL canvases and zero html2canvas snapshots.
      if (cssOnly || !ready) {
        surface.dataset.materialStatus = 'ready'
        continue
      }
      surface.dataset.materialStatus = 'loading'
      void ready.then(({ default: liquidGL }) => {
        if (disposed || bindings.get(surface) !== binding) return
        const compact = !!sidebarHost || surface.dataset.liquidSurface === 'control' || surface.dataset.liquidSurface === 'selection'
        const result = liquidGL({
          target: `#${lensTarget.id}`,
          snapshot: resolvedSnapshot,
          content: false, engine: 'auto', resolution: 1,
          zIndex: 0, refraction: 0, bevelDepth: compact && !sidebarHost ? 0.002 : 0.006,
          bevelWidth: compact ? 0.16 : 0.08, aberration: 0, magnify: 1, frost: 0,
          tint: dark && !sidebarHost ? 'rgba(100, 88, 116, 0.2)' : null,
          shadow: false, specular: !compact && !reducedMotion, reveal: 'none', tilt: false,
          on: { init: () => { if (!disposed && bindings.get(surface) === binding) surface.dataset.materialStatus = 'ready' } },
        })
        binding.lenses = Array.isArray(result) ? result : result ? [result] : []
        binding.canvases = [...new Set(binding.lenses.flatMap(lens => lens.renderer ? [lens.renderer.canvas] : []))]
        for (const canvas of binding.canvases) {
          let rim = rims.get(canvas)
          if (!rim) {
            rim = { mask: createGlassRim(canvas), targets: 0 }
            rims.set(canvas, rim)
          }
          rim.targets++
          rim.mask.track(lensTarget, compact ? 2 : 5, sidebarHost ? surface.closest<HTMLElement>('.session-rail-list') ?? undefined : undefined)
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
    if (changed && engine !== 'css') refresh()
  })
  observer.observe(root, { subtree: true, childList: true, characterData: engine !== 'css', attributes: true, attributeFilter: ['class', 'style', 'hidden', 'data-liquid-surface'] })
  const onScroll = (event: Event) => {
    if (event.target instanceof Element && event.target.matches('.session-rail-list')) {
      for (const { mask } of rims.values()) mask.update()
      return
    }
    refresh()
  }
  if (engine !== 'css') {
    root.addEventListener('scroll', onScroll, true)
    root.addEventListener('load', refresh, true)
  }
  window.addEventListener('resize', schedule)
  sync()
  return () => {
    disposed = true
    observer.disconnect()
    clearTimeout(refreshTimer)
    if (engine !== 'css') {
      root.removeEventListener('scroll', onScroll, true)
      root.removeEventListener('load', refresh, true)
    }
    window.removeEventListener('resize', schedule)
    for (const [surface, binding] of bindings) release(surface, binding)
  }
}
