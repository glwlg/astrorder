import { createHash } from 'node:crypto'
import {
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  statSync,
} from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { ASTRORDER_HOST_EXTERNALS } from '../packages/astrorder-plugin-sdk/vite-preset.mjs'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(scriptDir, '..')
const defaultSourceRoot = path.join(repoRoot, 'plugins', 'official')
const defaultOutRoot = path.join(repoRoot, 'plugins', 'official-dist')
const sharedNodeModulesDir = path.join(repoRoot, 'frontend', 'node_modules')

const pluginIdPattern = /^[a-z0-9][a-z0-9._-]{1,63}$/
const allowedExternals = new Set(ASTRORDER_HOST_EXTERNALS)
const allowedBackendTypes = new Set(['none', 'builtin', 'process', 'http'])

// Matches minified or formatted top-level static ES module import/export specifiers:
//   import{a}from"x", import a,{b}from"x", import*as a from"x", import"x", export{a}from"x", export*from"x"
const staticImportRe =
  /(?:^|[\r\n;])\s*(?:import|export)\s*(?:(?:\*\s*(?:as\s+[A-Za-z_$][\w$]*\s*)?|\{[^}]*\}|[A-Za-z_$][\w$]*(?:\s*,\s*(?:\*\s*as\s+[A-Za-z_$][\w$]*|\{[^}]*\}))?)\s*from\s*)?["']([^"']+)["']/g

function parseArgs(argv) {
  const opts = {
    sourceDir: defaultSourceRoot,
    outDir: defaultOutRoot,
    pluginFilter: null,
    checkOnly: false,
  }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--out-dir') {
      opts.outDir = path.resolve(argv[++i] || '')
    } else if (arg.startsWith('--out-dir=')) {
      opts.outDir = path.resolve(arg.slice('--out-dir='.length))
    } else if (arg === '--source-dir') {
      opts.sourceDir = path.resolve(argv[++i] || '')
    } else if (arg.startsWith('--source-dir=')) {
      opts.sourceDir = path.resolve(arg.slice('--source-dir='.length))
    } else if (arg === '--plugin') {
      opts.pluginFilter = (argv[++i] || '').trim()
    } else if (arg.startsWith('--plugin=')) {
      opts.pluginFilter = arg.slice('--plugin='.length).trim()
    } else if (arg === '--check-only') {
      opts.checkOnly = true
    } else {
      throw new Error(
        `Unknown argument: ${arg}. Usage: node scripts/build-official-plugins.mjs [--out-dir <dir>] [--source-dir <dir>] [--plugin <id>] [--check-only]`,
      )
    }
  }
  return opts
}

function isWithin(parent, child) {
  const rel = path.relative(parent, child)
  return rel !== '' && rel !== '..' && !rel.startsWith(`..${path.sep}`) && !path.isAbsolute(rel)
}

function validateRelativePluginPath(rel, label) {
  if (typeof rel !== 'string' || !rel.trim()) {
    throw new Error(`${label} must be a non-empty relative path`)
  }
  const trimmed = rel.trim()
  if (trimmed.includes('\0') || trimmed.includes('\\') || trimmed.startsWith('/') || path.isAbsolute(trimmed)) {
    throw new Error(`${label} must be a safe POSIX relative path: ${rel}`)
  }
  if (/^[a-zA-Z]:/.test(trimmed)) {
    throw new Error(`${label} must not contain a drive prefix: ${rel}`)
  }
  for (const seg of trimmed.split('/')) {
    if (seg === '..') {
      throw new Error(`${label} must not contain '..': ${rel}`)
    }
  }
  const normalized = path.posix.normalize(trimmed)
  if (normalized === '.' || normalized.startsWith('../') || normalized.startsWith('/')) {
    throw new Error(`${label} escapes plugin root: ${rel}`)
  }
  return normalized
}

export function validatePluginManifest(manifestPath) {
  if (!existsSync(manifestPath)) {
    throw new Error(`plugin.json missing: ${manifestPath}`)
  }
  const raw = readFileSync(manifestPath, 'utf8')
  let manifest
  try {
    manifest = JSON.parse(raw)
  } catch (err) {
    throw new Error(`Invalid JSON in ${manifestPath}: ${err.message}`)
  }
  if ((manifest.schema_version ?? 1) !== 1) {
    throw new Error(`Unsupported schema_version in ${manifestPath}: ${manifest.schema_version}`)
  }
  if (typeof manifest.id !== 'string' || !pluginIdPattern.test(manifest.id) || manifest.id.includes('..') || manifest.id === '_sdk') {
    throw new Error(`Invalid plugin id in ${manifestPath}: ${manifest.id}`)
  }
  if (typeof manifest.name !== 'string' || !manifest.name.trim()) {
    throw new Error(`Plugin name is required in ${manifestPath}`)
  }
  if (typeof manifest.version !== 'string' || !manifest.version.trim()) {
    throw new Error(`Plugin version is required in ${manifestPath}`)
  }
  const backendType =
    manifest.backend?.type === undefined || manifest.backend?.type === null || manifest.backend?.type === ''
      ? 'none'
      : typeof manifest.backend.type === 'string'
        ? manifest.backend.type.trim().toLowerCase()
        : String(manifest.backend.type)
  if (!allowedBackendTypes.has(backendType)) {
    throw new Error(
      `Unsupported backend.type in ${manifestPath}: ${manifest.backend?.type} (allowed: none, builtin, process, http)`,
    )
  }
  if (!manifest.frontend || manifest.frontend.mode !== 'esm') {
    throw new Error(`Official ESM plugin ${manifest.id} must declare frontend.mode === "esm"`)
  }
  const entry = validateRelativePluginPath(manifest.frontend.entry, `${manifest.id} frontend.entry`)
  const styles = Array.isArray(manifest.frontend.styles)
    ? manifest.frontend.styles.map((s, idx) =>
        validateRelativePluginPath(s, `${manifest.id} frontend.styles[${idx}]`),
      )
    : []
  return { manifest, entry, styles }
}

function assertConfinedNonEmptyFile(rootDir, relPosix, label) {
  const target = path.join(rootDir, ...relPosix.split('/'))
  if (!existsSync(target)) {
    throw new Error(`${label} missing: ${relPosix}`)
  }
  const resolvedRoot = realpathSync(rootDir)
  const resolvedTarget = realpathSync(target)
  if (!isWithin(resolvedRoot, resolvedTarget)) {
    throw new Error(`${label} escapes plugin dist root: ${relPosix}`)
  }
  const info = statSync(resolvedTarget)
  if (!info.isFile() || info.size === 0) {
    throw new Error(`${label} must be a non-empty file: ${relPosix}`)
  }
  return { resolvedTarget, size: info.size }
}

function extractStaticModuleSpecifiers(jsSource) {
  const specifiers = new Set()
  for (const match of jsSource.matchAll(staticImportRe)) {
    const spec = match[1]
    if (spec) specifiers.add(spec)
  }
  return [...specifiers].sort()
}

export function verifyBuiltPluginDist(distDir) {
  const resolvedDist = realpathSync(path.resolve(distDir))
  const { manifest, entry, styles } = validatePluginManifest(path.join(resolvedDist, 'plugin.json'))

  const entryInfo = assertConfinedNonEmptyFile(resolvedDist, entry, `${manifest.id} entry`)
  const entrySource = readFileSync(entryInfo.resolvedTarget, 'utf8')
  const specifiers = extractStaticModuleSpecifiers(entrySource)

  const externalImports = []
  for (const spec of specifiers) {
    if (
      spec.startsWith('/') ||
      spec.startsWith('file://') ||
      /^[a-zA-Z]:[\\/]/.test(spec) ||
      spec.includes('node_modules')
    ) {
      throw new Error(
        `Plugin ${manifest.id} (${entry}) contains forbidden absolute or node_modules import: ${spec}`,
      )
    }
    if (spec.startsWith('./') || spec.startsWith('../')) {
      const resolvedChunk = path.posix.normalize(path.posix.join(path.posix.dirname(entry), spec))
      assertConfinedNonEmptyFile(resolvedDist, resolvedChunk, `${manifest.id} chunk ${spec}`)
      continue
    }
    if (!allowedExternals.has(spec)) {
      throw new Error(
        `Plugin ${manifest.id} (${entry}) has unmapped external bare import "${spec}". Allowed host externals: ${[...allowedExternals].join(', ')}`,
      )
    }
    externalImports.push(spec)
  }

  if (!/\bexport\b/.test(entrySource)) {
    throw new Error(`Plugin ${manifest.id} (${entry}) does not contain any ESM export`)
  }
  if (!entrySource.includes('activate')) {
    throw new Error(`Plugin ${manifest.id} (${entry}) does not reference an activate export`)
  }

  const verifiedStyles = styles.map((styleRel) => {
    const info = assertConfinedNonEmptyFile(resolvedDist, styleRel, `${manifest.id} style`)
    return { path: styleRel, size: info.size }
  })

  const sha256 = createHash('sha256').update(entrySource).digest('hex')

  return {
    id: manifest.id,
    version: manifest.version,
    entry,
    entrySize: entryInfo.size,
    entrySha256: sha256,
    styles: verifiedStyles,
    externalImports,
    distDir: resolvedDist,
  }
}

async function loadViteBuild() {
  const viteEntry = path.join(sharedNodeModulesDir, 'vite', 'dist', 'node', 'index.js')
  if (!existsSync(viteEntry)) {
    throw new Error(`Vite not found at ${viteEntry}; install frontend dependencies first.`)
  }
  const viteMod = await import(pathToFileURL(viteEntry).href)
  if (typeof viteMod.build !== 'function') {
    throw new Error(`Invalid Vite module at ${viteEntry}: missing build()`)
  }
  return viteMod.build
}

function discoverOfficialPlugins(sourceDir, pluginFilter) {
  if (!existsSync(sourceDir)) {
    throw new Error(`Official plugins source directory does not exist: ${sourceDir}`)
  }
  const entries = readdirSync(sourceDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && !entry.name.startsWith('.'))
    .map((entry) => path.join(sourceDir, entry.name))
    .filter((dir) => existsSync(path.join(dir, 'plugin.json')))
    .sort()

  if (!pluginFilter) return entries
  const filtered = entries.filter((dir) => {
    const { manifest } = validatePluginManifest(path.join(dir, 'plugin.json'))
    return manifest.id === pluginFilter || path.basename(dir) === pluginFilter
  })
  if (filtered.length === 0) {
    throw new Error(`Official plugin "${pluginFilter}" not found under ${sourceDir}`)
  }
  return filtered
}

export async function buildOfficialPlugins(options = {}) {
  const sourceDir = path.resolve(options.sourceDir || defaultSourceRoot)
  const outDir = path.resolve(options.outDir || defaultOutRoot)
  const checkOnly = Boolean(options.checkOnly)
  const pluginDirs = discoverOfficialPlugins(sourceDir, options.pluginFilter || null)

  if (pluginDirs.length === 0) {
    throw new Error(`No official ESM plugins found under ${sourceDir}`)
  }

  const viteBuild = checkOnly ? null : await loadViteBuild()
  process.env.ASTRORDER_SHARED_NODE_MODULES = sharedNodeModulesDir

  mkdirSync(outDir, { recursive: true })

  const results = []
  for (const pluginDir of pluginDirs) {
    const { manifest } = validatePluginManifest(path.join(pluginDir, 'plugin.json'))
    const pluginDistDir = path.join(pluginDir, 'dist')
    const targetOutDir = path.join(outDir, manifest.id)

    if (!checkOnly) {
      const configPath = path.join(pluginDir, 'vite.config.ts')
      const mjsConfigPath = path.join(pluginDir, 'vite.config.mjs')
      const configFile = existsSync(configPath)
        ? configPath
        : existsSync(mjsConfigPath)
          ? mjsConfigPath
          : false

      if (!configFile) {
        throw new Error(`Plugin ${manifest.id} is missing vite.config.ts / vite.config.mjs`)
      }

      await viteBuild({
        root: pluginDir,
        configFile,
        mode: 'production',
        logLevel: 'warn',
      })

      // Ensure plugin.json is always present in dist even if custom config omitted copy hook.
      const distManifestPath = path.join(pluginDistDir, 'plugin.json')
      if (!existsSync(distManifestPath)) {
        cpSync(path.join(pluginDir, 'plugin.json'), distManifestPath)
      }

      verifyBuiltPluginDist(pluginDistDir)

      rmSync(targetOutDir, { recursive: true, force: true })
      mkdirSync(targetOutDir, { recursive: true })
      cpSync(pluginDistDir, targetOutDir, { recursive: true })
    }

    const verified = verifyBuiltPluginDist(targetOutDir)
    results.push(verified)
  }

  return {
    ok: true,
    sourceDir,
    outDir,
    count: results.length,
    plugins: results,
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const opts = parseArgs(process.argv.slice(2))
    const summary = await buildOfficialPlugins(opts)
    console.log(JSON.stringify(summary, null, 2))
  } catch (err) {
    console.error(`[build-official-plugins] FAILED: ${err instanceof Error ? err.message : String(err)}`)
    process.exit(1)
  }
}
