import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { verifyFrontendDist } from './verify-frontend-dist.mjs'

const scriptPath = path.join(path.dirname(fileURLToPath(import.meta.url)), 'verify-frontend-dist.mjs')

function makeFixture(t, { withSw = true, withAssets = true, withIndex = true, indexHtml } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'astrorder-dist-gate-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  writeFileSync(path.join(dir, 'favicon.ico'), 'ico')
  if (withAssets) {
    mkdirSync(path.join(dir, 'assets'), { recursive: true })
    writeFileSync(path.join(dir, 'assets', 'index-abc.js'), 'console.log("ok")')
    writeFileSync(path.join(dir, 'assets', 'index-abc.css'), 'body{}')
  }
  if (withIndex) {
    writeFileSync(
      path.join(dir, 'index.html'),
      indexHtml ?? '<!doctype html><html><head><script type="module" crossorigin src="/assets/index-abc.js"></script><link rel="stylesheet" href="/assets/index-abc.css"></head><body></body></html>',
    )
  }
  if (withSw) {
    writeFileSync(path.join(dir, 'sw-astrorder.js'), 'self.addEventListener("fetch",()=>{})')
  }
  return dir
}

test('accepts complete browser and desktop frontend dist bundles', t => {
  const browserDir = makeFixture(t, { withSw: true })
  const browserSummary = verifyFrontendDist(browserDir, { mode: 'browser' })
  assert.equal(browserSummary.mode, 'browser')
  assert.equal(browserSummary.assetCount, 2)
  assert.equal(browserSummary.referencedAssetCount, 2)
  assert.equal(browserSummary.hasServiceWorker, true)

  const desktopDir = makeFixture(t, { withSw: false })
  const desktopSummary = verifyFrontendDist(desktopDir, { mode: 'desktop' })
  assert.equal(desktopSummary.mode, 'desktop')
  assert.equal(desktopSummary.hasServiceWorker, false)
})

test('rejects public-only dist missing index.html', t => {
  const dir = makeFixture(t, { withIndex: false, withAssets: false, withSw: false })
  assert.throws(() => verifyFrontendDist(dir, { mode: 'browser' }), /index\.html/)
})

test('rejects unbuilt source index.html', t => {
  const dir = makeFixture(t, {
    indexHtml: '<!doctype html><script type="module" src="/src/main.tsx"></script>',
  })
  assert.throws(() => verifyFrontendDist(dir, { mode: 'browser' }), /unbuilt \/src\//)
})

test('rejects missing referenced asset bundle', t => {
  const dir = makeFixture(t)
  rmSync(path.join(dir, 'assets', 'index-abc.css'))
  assert.throws(() => verifyFrontendDist(dir, { mode: 'browser' }), /index-abc\.css/)
})

test('enforces browser vs desktop PWA worker separation', t => {
  const wailsDir = makeFixture(t, { withSw: false })
  assert.throws(() => verifyFrontendDist(wailsDir, { mode: 'browser' }), /sw-astrorder\.js/)

  const pwaDir = makeFixture(t, { withSw: true })
  assert.throws(() => verifyFrontendDist(pwaDir, { mode: 'desktop' }), /sw-astrorder\.js/)
})

test('CLI returns zero for valid dist and non-zero for incomplete dist', t => {
  const validDir = makeFixture(t, { withSw: true })
  const ok = spawnSync(process.execPath, [scriptPath, '--mode', 'browser', validDir], { encoding: 'utf8' })
  assert.equal(ok.status, 0, ok.stderr)
  assert.equal(JSON.parse(ok.stdout).ok, true)

  const brokenDir = makeFixture(t, { withIndex: false })
  const fail = spawnSync(process.execPath, [scriptPath, '--mode', 'browser', brokenDir], { encoding: 'utf8' })
  assert.notEqual(fail.status, 0)
  assert.match(fail.stderr, /index\.html/)
})
