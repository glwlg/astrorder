import type { PluginOption, UserConfig } from 'vite'

export declare const ASTRORDER_HOST_EXTERNALS: readonly [
  'react',
  'react/jsx-runtime',
  'react-dom',
  'react-dom/client',
  '@mantine/core',
  '@mantine/notifications',
  '@astrorder/sdk',
]

export interface AstrorderExternalPluginOptions {
  extraExternals?: string[]
  sharedNodeModulesDir?: string | null
}

export interface AstrorderPluginBuildOptions {
  root?: string
  entry?: string
  outDir?: string
  entryFileName?: string
  cssFileName?: string
  manifestFile?: string | false
  extraExternals?: string[]
  sharedNodeModulesDir?: string
  plugins?: PluginOption[]
  define?: Record<string, string>
  target?: string
  minify?: boolean | 'esbuild' | 'terser' | 'oxc'
  sourcemap?: boolean | 'inline' | 'hidden'
  chunkSizeWarningLimit?: number
}

export declare function vitePluginAstrorderExternal(
  options?: AstrorderExternalPluginOptions,
): PluginOption

export declare function createAstrorderPluginConfig(
  options?: AstrorderPluginBuildOptions,
): UserConfig
