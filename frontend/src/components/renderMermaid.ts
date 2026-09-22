import mermaid from 'mermaid'

mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'default' })

let nextId = 0
let renderQueue = Promise.resolve()

export function renderMermaid(code: string) {
  const render = renderQueue.then(async () => {
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      return await Promise.race([
        mermaid.render(`mermaid-${++nextId}`, code),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error('图表渲染超时')), 8000)
        }),
      ])
    } finally {
      clearTimeout(timer)
    }
  })

  renderQueue = render.then(() => undefined, () => undefined)
  return render
}
