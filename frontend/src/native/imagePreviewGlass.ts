import { installLiquidSurfaces } from '../components/liquidSurfaces'
import materialCss from '../components/liquidSurfaces.css?inline'

document.documentElement.dataset.previewTheme = 'dark'
// This window is served by Wails, outside Vite's HTML entry and CSS injection.
const style = document.createElement('style')
style.textContent = materialCss
document.head.append(style)
const dispose = installLiquidSurfaces({ snapshot: '#stage', dark: true, reducedMotion: matchMedia('(prefers-reduced-motion: reduce)').matches })
window.addEventListener('pagehide', dispose, { once: true })
