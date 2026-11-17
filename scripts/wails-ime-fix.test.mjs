import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { patchWailsActivation, prepareWailsImeFix } from './wails-ime-fix.mjs'

const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'desktop-wails')
const env = { ...process.env, GOWORK: 'off', GOOS: 'windows', GOARCH: 'amd64', CGO_ENABLED: '0' }
const moduleName = 'github.com/wailsapp/wails/v3'
const sourcePath = path.join('pkg', 'application', 'webview_window_windows.go')

function moduleInfo(args = []) {
  const result = spawnSync('go', ['list', ...args, '-m', '-json', moduleName], { cwd: desktop, env, encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  return JSON.parse(result.stdout)
}

test('patches only activation focus calls and retains the regular focus handling', () => {
  const source = readFileSync(path.join(moduleInfo().Dir, sourcePath))
  const patched = patchWailsActivation(source)
  const sourceText = source.toString('utf8')
  assert.equal(sourceText.split('\t\t\tw.restoreWebviewFocus()\n').length - 1, 2)
  assert.equal(patched, sourceText.replaceAll('\t\t\tw.restoreWebviewFocus()\n', ''))
  assert.match(patched, /case w32\.WM_SETFOCUS:\s+if w\.focusHandoff\.shouldRestoreWebview\(\) {\s+w\.focus\(\)/)
  assert.match(patched, /chromium\.MoveFocusRequestedCallback = w\.moveFocusRequested/)
})

test('rejects an unexpected upstream source instead of building without the fix', () => {
  const source = readFileSync(path.join(moduleInfo().Dir, sourcePath))
  assert.throws(() => patchWailsActivation(Buffer.concat([source, Buffer.from('\n')])), /Unexpected Wails/)
})

test('build replacement is isolated and leaves the module cache unchanged', () => {
  const originalPath = path.join(moduleInfo().Dir, sourcePath)
  const digest = () => createHash('sha256').update(readFileSync(originalPath)).digest('hex')
  const before = digest()
  const fix = prepareWailsImeFix(desktop, env)
  let copiedPath
  try {
    const module = moduleInfo(['-modfile', fix.modfile])
    assert.equal(module.Version, 'v3.0.0')
    copiedPath = path.join(module.Replace.Dir, sourcePath)
    assert.equal(readFileSync(copiedPath, 'utf8'), patchWailsActivation(readFileSync(originalPath)))
    assert.equal(digest(), before)
  } finally {
    fix.cleanup()
  }
  assert.equal(existsSync(copiedPath), false)
  assert.equal(digest(), before)
})
