import { copyFileSync, existsSync, mkdirSync } from 'node:fs'
import path from 'node:path'

/**
 * Host-provided bare module specifiers mapped by Astrorder's scoped Import Map
 * under `/api/v1/ext/`.
 *
 * NOTE: `@tabler/icons-react` is intentionally excluded to preserve host
 * tree-shaking. Plugins should inline the 2-5 lightweight SVG icons they need.
 */
export const ASTRORDER_HOST_EXTERNALS = Object.freeze([
  'react',
  'react/jsx-runtime',
  'react-dom',
  'react-dom/client',
  '@mantine/core',
  '@mantine/notifications',
  '@astrorder/sdk',
  'react-router-dom',
  '@tanstack/react-query',
  '@astrorder/workspace-bridge',
])

function buildExternalMatcher(extraExternals = []) {
  const exact = new Set([...ASTRORDER_HOST_EXTERNALS, ...extraExternals])
  return (id) => exact.has(id)
}

function detectSharedNodeModulesDir(rootDir, explicitDir) {
  if (explicitDir && existsSync(explicitDir)) {
    return path.resolve(explicitDir)
  }
  if (process.env.ASTRORDER_SHARED_NODE_MODULES && existsSync(process.env.ASTRORDER_SHARED_NODE_MODULES)) {
    return path.resolve(process.env.ASTRORDER_SHARED_NODE_MODULES)
  }
  let cursor = path.resolve(rootDir)
  for (let i = 0; i < 6; i++) {
    const candidate = path.join(cursor, 'frontend', 'node_modules')
    if (existsSync(candidate)) {
      return candidate
    }
    const parent = path.dirname(cursor)
    if (parent === cursor) break
    cursor = parent
  }
  return null
}

/**
 * Vite plugin that:
 * 1. Marks Astrorder host runtime modules as Rollup/Rolldown externals so they
 *    remain bare ES imports resolved via `/api/v1/ext/_sdk/*.js`.
 * 2. In monorepo/offline environments, transparently falls back to a shared
 *    `node_modules` directory (e.g. `frontend/node_modules`) for plugin-private
 *    dependencies like `three` without leaking absolute paths into output JS.
 */
export function vitePluginAstrorderExternal(options = {}) {
  const isExternal = buildExternalMatcher(options.extraExternals)
  const sharedNodeModulesDir = options.sharedNodeModulesDir || null
  const virtualImporter = sharedNodeModulesDir
    ? path.join(sharedNodeModulesDir, '__astrorder_plugin_resolver__.js')
    : null

  return {
    name: 'astrorder-plugin-external',
    enforce: 'pre',
    async resolveId(source, importer, resolveOptions) {
      if (isExternal(source)) {
        return { id: source, external: true }
      }
      if (!importer || !virtualImporter) return null
      if (
        source.startsWith('.') ||
        source.startsWith('/') ||
        source.startsWith('\0') ||
        source.startsWith('node:') ||
        path.isAbsolute(source)
      ) {
        return null
      }
      const local = await this.resolve(source, importer, {
        ...resolveOptions,
        skipSelf: true,
      })
      if (local && !local.id.endsWith('.d.ts') && !local.id.endsWith('.d.mts')) {
        return local
      }
      return this.resolve(source, virtualImporter, {
        ...resolveOptions,
        skipSelf: true,
      })
    },
  }
}

function astrorderCopyManifestPlugin(rootDir, outDir, manifestFile) {
  let resolvedOutDir = path.resolve(rootDir, outDir)
  return {
    name: 'astrorder-copy-manifest',
    configResolved(config) {
      resolvedOutDir = path.isAbsolute(config.build.outDir)
        ? config.build.outDir
        : path.resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      if (!manifestFile) return
      const src = path.isAbsolute(manifestFile)
        ? manifestFile
        : path.resolve(rootDir, manifestFile)
      if (!existsSync(src)) return
      mkdirSync(resolvedOutDir, { recursive: true })
      copyFileSync(src, path.join(resolvedOutDir, 'plugin.json'))
    },
  }
}

/**
 * Standard Vite configuration factory for Astrorder in-page ESM plugins.
 * Produces clean ES Module output at `ui/entry.js` and `ui/entry.css`,
 * externalizing shared host dependencies and bundling plugin-private code.
 */
export function createAstrorderPluginConfig(options = {}) {
  const rootDir = path.resolve(options.root || process.cwd())
  const entry = options.entry || 'src/entry.tsx'
  const outDir = options.outDir || 'dist'
  const entryFileName = options.entryFileName || 'ui/entry.js'
  const cssFileName = options.cssFileName || 'ui/entry.css'
  const manifestFile = options.manifestFile === false ? null : options.manifestFile || 'plugin.json'
  const extraExternals = options.extraExternals || []
  const isExternal = buildExternalMatcher(extraExternals)
  const sharedNodeModulesDir = detectSharedNodeModulesDir(rootDir, options.sharedNodeModulesDir)
  const cssFileBaseName = cssFileName.endsWith('.css')
    ? cssFileName.slice(0, -'.css'.length)
    : cssFileName

  const plugins = [
    vitePluginAstrorderExternal({
      extraExternals,
      sharedNodeModulesDir,
    }),
    ...(options.plugins || []),
    astrorderCopyManifestPlugin(rootDir, outDir, manifestFile),
  ]

  return {
    root: rootDir,
    mode: 'production',
    base: './',
    plugins,
    oxc: {
      jsx: {
        runtime: 'automatic',
        importSource: 'react',
        development: false,
      },
    },
    define: {
      'process.env.NODE_ENV': JSON.stringify('production'),
      ...(options.define || {}),
    },
    build: {
      outDir,
      emptyOutDir: true,
      target: options.target || 'es2022',
      cssCodeSplit: false,
      minify: options.minify ?? true,
      sourcemap: options.sourcemap ?? false,
      chunkSizeWarningLimit: options.chunkSizeWarningLimit ?? 2000,
      lib: {
        entry: path.resolve(rootDir, entry),
        formats: ['es'],
        fileName: () => entryFileName,
        cssFileName: cssFileBaseName,
      },
      rolldownOptions: {
        external: (id) => isExternal(id),
        output: {
          minify: options.minify ?? true,
          comments: false,
          entryFileNames: entryFileName,
          chunkFileNames: 'ui/chunks/[name]-[hash].js',
          assetFileNames: (assetInfo) => {
            const names = assetInfo.names || (assetInfo.name ? [assetInfo.name] : [])
            if (names.some((name) => name.endsWith('.css'))) {
              return cssFileName
            }
            return 'ui/assets/[name]-[hash][extname]'
          },
        },
      },
    },
  }
}
