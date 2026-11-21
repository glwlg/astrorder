/** Keep liquidGL's snapshot on the rim; the centre uses the live DOM backdrop. */
export function createGlassRim(canvas: HTMLCanvasElement) {
  const targets = new Map<HTMLElement, { width: number; scrollport?: HTMLElement }>()
  const original = {
    maskImage: canvas.style.maskImage,
    maskSize: canvas.style.maskSize,
    maskRepeat: canvas.style.maskRepeat,
    maskMode: canvas.style.maskMode,
  }

  let writingMask = false
  const update = () => {
    const bounds = canvas.getBoundingClientRect()
    const rings: string[] = []
    const clips = new Map<HTMLElement, string>()
    const clipPaths: string[] = []
    for (const [target, { width, scrollport }] of targets) {
      const rect = target.getBoundingClientRect()
      if (rect.width <= 0 || rect.height <= 0) continue
      const stroke = Math.min(width, rect.width / 2, rect.height / 2)
      const radius = Math.min(
        Number.parseFloat(getComputedStyle(target).borderTopLeftRadius),
        rect.width / 2, rect.height / 2,
      )
      const ring = `<rect x="${rect.left - bounds.left + stroke / 2}" y="${rect.top - bounds.top + stroke / 2}" width="${rect.width - stroke}" height="${rect.height - stroke}" rx="${Math.max(0, radius - stroke / 2)}" fill="none" stroke="white" stroke-width="${stroke}"/>`
      if (!scrollport) {
        rings.push(ring)
        continue
      }
      const clip = scrollport.getBoundingClientRect()
      if (rect.bottom <= clip.top || rect.top >= clip.bottom) continue
      let id = clips.get(scrollport)
      if (!id) {
        id = `scrollport-${clips.size}`
        clips.set(scrollport, id)
        clipPaths.push(`<clipPath id="${id}"><rect x="${clip.left - bounds.left}" y="${clip.top - bounds.top}" width="${scrollport.clientWidth}" height="${scrollport.clientHeight}"/></clipPath>`)
      }
      rings.push(`<g clip-path="url(#${id})">${ring}</g>`)
    }
    const image = bounds.width > 0 && bounds.height > 0 && rings.length > 0
      ? `url("data:image/svg+xml,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${bounds.width}" height="${bounds.height}" viewBox="0 0 ${bounds.width} ${bounds.height}"><defs>${clipPaths.join('')}</defs>${rings.join('')}</svg>`)}")`
      : 'linear-gradient(transparent, transparent)'
    if (canvas.style.maskImage !== image) {
      writingMask = true
      canvas.style.maskImage = image
      writingMask = false
    }
  }
  Object.assign(canvas.style, { maskSize: '100% 100%', maskRepeat: 'no-repeat', maskMode: 'alpha' })
  update()
  const resize = new ResizeObserver(update)
  resize.observe(canvas)
  // liquidGL also adjusts the canvas origin when its scroll anchor moves.
  const position = new MutationObserver(() => {
    if (!writingMask) update()
  })
  position.observe(canvas, { attributes: true, attributeFilter: ['style'] })

  return {
    track(target: HTMLElement, width: number, scrollport?: HTMLElement) {
      targets.set(target, { width, scrollport })
      resize.observe(target)
      update()
    },
    untrack(target: HTMLElement) {
      targets.delete(target)
      resize.unobserve(target)
      update()
    },
    update,
    destroy() {
      resize.disconnect()
      position.disconnect()
      Object.assign(canvas.style, original)
    },
  }
}
