import { existsSync, readdirSync, readFileSync, realpathSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const unbuiltSourceRefRe = /\b(?:src|href)\s*=\s*["'](?:\.?\/)?src\/[^"']+["']/i
const builtAssetRefRe = /\b(?:src|href)\s*=\s*["']((?:\.?\/)?assets\/[^"'?#]+)(?:[?#][^"']*)?["']/gi
const moduleEntryBeforeSrcRe = /<script\b[^>]*\btype\s*=\s*["']module["'][^>]*\bsrc\s*=\s*["'](?:\.?\/)?assets\/[^"'?#]+/i
const moduleEntryBeforeTypeRe = /<script\b[^>]*\bsrc\s*=\s*["'](?:\.?\/)?assets\/[^"'?#]+["'][^>]*\btype\s*=\s*["']module["']/i
const browserServiceWorkerFile = 'sw-astrorder.js'

function isWithin(parent, child) {
  const rel = path.relative(parent, child)
  return rel !== '' && rel !== '..' && !rel.startsWith(`..${path.sep}`) && !path.isAbsolute(rel)
}

function assertConfinedNonEmptyFile(resolvedRoot, relativePath, label) {
  const candidate = path.join(resolvedRoot, relativePath)
  if (!existsSync(candidate)) {
    throw new Error(`${label} missing: ${relativePath}`)
  }
  const resolved = realpathSync(candidate)
  if (!isWithin(resolvedRoot, resolved)) {
    throw new Error(`${label} escapes dist directory: ${relativePath}`)
  }
  const info = statSync(resolved)
  if (!info.isFile()) {
    throw new Error(`${label} is not a regular file: ${relativePath}`)
  }
  if (info.size === 0) {
    throw new Error(`${label} is empty: ${relativePath}`)
  }
  return resolved
}

export function verifyFrontendDist(dir, { mode = 'browser' } = {}) {
  if (mode !== 'browser' && mode !== 'desktop') {
    throw new Error(`Invalid frontend dist verification mode: ${mode} (expected "browser" or "desktop")`)
  }
  if (!dir || !existsSync(dir)) {
    throw new Error(`Frontend dist directory does not exist: ${dir}`)
  }
  const resolvedRoot = realpathSync(path.resolve(dir))
  if (!statSync(resolvedRoot).isDirectory()) {
    throw new Error(`Frontend dist path is not a directory: ${dir}`)
  }

  const indexPath = assertConfinedNonEmptyFile(resolvedRoot, 'index.html', 'Frontend dist entry')
  const indexHtml = readFileSync(indexPath, 'utf8')
  if (unbuiltSourceRefRe.test(indexHtml)) {
    throw new Error(`Frontend dist index.html references unbuilt /src/ assets: ${indexPath}`)
  }
  if (!moduleEntryBeforeSrcRe.test(indexHtml) && !moduleEntryBeforeTypeRe.test(indexHtml)) {
    throw new Error(`Frontend dist index.html is missing a compiled module script entry under /assets/: ${indexPath}`)
  }

  const assetsCandidate = path.join(resolvedRoot, 'assets')
  if (!existsSync(assetsCandidate)) {
    throw new Error(`Frontend dist is missing assets/ directory: ${resolvedRoot}`)
  }
  const resolvedAssets = realpathSync(assetsCandidate)
  if (!isWithin(resolvedRoot, resolvedAssets) || !statSync(resolvedAssets).isDirectory()) {
    throw new Error(`Frontend dist assets/ must be a confined directory: ${resolvedRoot}`)
  }
  const assetEntries = readdirSync(resolvedAssets)
  let regularAssetCount = 0
  for (const name of assetEntries) {
    const info = statSync(path.join(resolvedAssets, name))
    if (info.isFile() && info.size > 0) regularAssetCount++
  }
  if (regularAssetCount === 0) {
    throw new Error(`Frontend dist assets/ contains no compiled bundle files: ${resolvedAssets}`)
  }

  const referenced = new Set()
  for (const match of indexHtml.matchAll(builtAssetRefRe)) {
    const rawRef = match[1].replace(/\\/g, '/')
    const trimmed = rawRef.replace(/^\.?\//, '')
    const normalized = path.posix.normalize(`/${trimmed}`)
    if (!normalized.startsWith('/assets/')) {
      throw new Error(`Frontend dist index.html references invalid asset path: ${match[1]}`)
    }
    const rel = normalized.slice(1)
    if (referenced.has(rel)) continue
    referenced.add(rel)
    assertConfinedNonEmptyFile(resolvedRoot, path.join(...rel.split('/')), 'Referenced bundle asset')
  }

  const swCandidate = path.join(resolvedRoot, browserServiceWorkerFile)
  const hasServiceWorker = existsSync(swCandidate)
  if (mode === 'browser') {
    assertConfinedNonEmptyFile(
      resolvedRoot,
      browserServiceWorkerFile,
      'Browser PWA worker (rebuild without ASTRORDER_WAILS=1)',
    )
  } else if (hasServiceWorker) {
    throw new Error(`Desktop embedded frontend must not contain PWA worker ${browserServiceWorkerFile}: ${resolvedRoot}`)
  }

  return {
    dir: resolvedRoot,
    mode,
    assetCount: regularAssetCount,
    referencedAssetCount: referenced.size,
    hasServiceWorker: mode === 'browser',
  }
}

function runCli(argv) {
  let mode = 'browser'
  let target = ''
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--mode') {
      mode = argv[++i] || ''
    } else if (arg.startsWith('--mode=')) {
      mode = arg.slice('--mode='.length)
    } else if (!target) {
      target = arg
    } else {
      throw new Error(`Unexpected argument: ${arg}`)
    }
  }
  if (!target) {
    throw new Error('Usage: node scripts/verify-frontend-dist.mjs [--mode browser|desktop] <dist-dir>')
  }
  const result = verifyFrontendDist(target, { mode })
  console.log(JSON.stringify({ ok: true, ...result }))
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    runCli(process.argv.slice(2))
  } catch (err) {
    console.error(err.message)
    process.exit(1)
  }
}
