declare module 'liquid-gl' {
  export interface LiquidGlassLens {
    destroy(): void
    // Adapter for pinned liquid-gl 3.0.0; needed for nested scroll containers.
    renderer: { canvas: HTMLCanvasElement; captureSnapshot(): Promise<boolean | undefined> } | null
  }

  export interface LiquidGlassOptions {
    target: string
    snapshot: string
    content: false
    engine: 'auto'
    resolution: number
    zIndex: number
    refraction: number
    aberration: number
    bevelDepth: number
    bevelWidth: number
    magnify: number
    frost: number
    tint: string | null
    shadow: boolean
    specular: boolean
    reveal: 'none'
    tilt: boolean
    on: { init: () => void }
  }

  export default function liquidGL(options: LiquidGlassOptions): LiquidGlassLens | LiquidGlassLens[] | undefined
}
