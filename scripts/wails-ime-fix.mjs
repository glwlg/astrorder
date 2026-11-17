import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, copyFileSync, cpSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const moduleName = 'github.com/wailsapp/wails/v3'
const patchedVersion = 'v3.0.0'
const windowSource = path.join('pkg', 'application', 'webview_window_windows.go')
const windowSourceHash = '4efc69a4af30eb970313142c3c61f6c14a50aa7f0a3c5a8271da036f0e7f0572'
const focusCall = '\t\t\tw.restoreWebviewFocus()\n'

// Wails 3.0.0 calls MoveFocus while WM_ACTIVATE is still being processed.
// With Windows Chinese IMEs this can put composition at the screen origin.
// Keep WM_SETFOCUS and the remaining keyboard-focus handling intact.
export function patchWailsActivation(source) {
  const digest = createHash('sha256').update(source).digest('hex')
  if (digest !== windowSourceHash) throw new Error(`Unexpected Wails ${patchedVersion} window source: ${digest}`)
  const parts = source.toString('utf8').split(focusCall)
  if (parts.length !== 3) throw new Error('Expected two Wails activation focus calls')
  return parts.join('')
}

function go(args, cwd, env) {
  const result = spawnSync('go', args, { cwd, env, encoding: 'utf8', windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`go ${args.join(' ')} failed: ${(result.stderr || result.stdout).trim()}`)
  return result.stdout
}

// Go module-cache files are read-only. The isolated copy must be writable both
// for the patch and for reliable cleanup on Windows and POSIX.
function makeWritable(target) {
  const stat = lstatSync(target)
  if (stat.isSymbolicLink()) return
  chmodSync(target, stat.isDirectory() ? 0o700 : 0o600)
  if (stat.isDirectory()) for (const name of readdirSync(target)) makeWritable(path.join(target, name))
}

export function prepareWailsImeFix(desktop, baseEnv) {
  const env = { ...baseEnv, GOWORK: 'off' }
  const module = JSON.parse(go(['list', '-m', '-json', moduleName], desktop, env))
  if (module.Version !== patchedVersion || module.Replace || !module.Dir) {
    throw new Error(`Wails IME fix requires the unmodified ${moduleName} ${patchedVersion} module`)
  }
  const original = readFileSync(path.join(module.Dir, windowSource))
  const patched = patchWailsActivation(original)
  const scratch = mkdtempSync(path.join(os.tmpdir(), 'astrorder-wails-ime-'))
  const cleanup = () => {
    makeWritable(scratch)
    rmSync(scratch, { recursive: true })
  }
  try {
    const copy = path.join(scratch, 'wails')
    mkdirSync(copy)
    for (const name of ['go.mod', 'go.sum', 'LICENSE']) copyFileSync(path.join(module.Dir, name), path.join(copy, name))
    for (const name of ['pkg', 'internal']) cpSync(path.join(module.Dir, name), path.join(copy, name), { recursive: true })
    const target = path.join(copy, windowSource)
    chmodSync(target, 0o600)
    writeFileSync(target, patched)
    const modfile = path.join(scratch, 'desktop.mod')
    copyFileSync(path.join(desktop, 'go.mod'), modfile)
    copyFileSync(path.join(desktop, 'go.sum'), path.join(scratch, 'desktop.sum'))
    go(['mod', 'edit', '-modfile', modfile, `-replace=${moduleName}=${copy}`], desktop, env)
    return { env, modfile, cleanup }
  } catch (error) {
    cleanup()
    throw error
  }
}
