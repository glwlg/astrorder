import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createAstrorderPluginConfig } from '../../../packages/astrorder-plugin-sdk/vite-preset.mjs'

const pluginRoot = path.dirname(fileURLToPath(import.meta.url))

export default createAstrorderPluginConfig({
  root: pluginRoot,
  entry: 'src/entry.tsx',
  outDir: 'dist',
  entryFileName: 'ui/entry.js',
  cssFileName: 'ui/entry.css',
  manifestFile: 'plugin.json',
})
