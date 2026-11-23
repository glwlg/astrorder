import { spawnSync } from 'node:child_process'
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { prepareWailsImeFix } from './wails-ime-fix.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const desktop = path.join(root, 'desktop-wails')
const tools = path.join(root, '.tools', 'wails')
const versions = JSON.parse(readFileSync(path.join(desktop, 'toolchain.json'), 'utf8'))
const windows = process.platform === 'win32'
const nodeDir = path.join(tools, `node-v${versions.node}-win-x64`)
const node = windows ? path.join(nodeDir, 'node.exe') : process.execPath
const npm = windows ? path.join(nodeDir, 'node_modules', 'npm', 'bin', 'npm-cli.js') : 'npm'
const wails = windows ? path.join(tools, 'bin', 'wails3.exe') : 'wails3'
const action = process.argv[2] || 'check'
const env = { ...process.env, GOTOOLCHAIN: `go${versions.go}`, ASTRORDER_WAILS: '1' }
if (windows) env.PATH = `${nodeDir}${path.delimiter}${path.join(tools, 'bin')}${path.delimiter}${process.env.PATH}`
const windowsEnv = { ...env, GOOS: 'windows', GOARCH: 'amd64', CGO_ENABLED: '0' }
function run(exe, args, cwd = root, extra = {}) {
  const result = spawnSync(exe, args, { cwd, env, stdio: 'inherit', windowsHide: true, ...extra })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${path.basename(exe)} ${args.join(' ')} exited ${result.status}`)
  return result
}
function bindings() { run(wails, ['generate', 'bindings', '-b', '-i', '-names', '-d', 'assets/bindings', '.'], desktop, { env: windowsEnv }) }
function runNpm(args) { run(windows ? node : npm, windows ? [npm, ...args] : args, path.join(root, 'frontend')) }
function withWailsImeFix(callback) {
  const fix = prepareWailsImeFix(desktop, windowsEnv)
  try { callback(fix) } finally { fix.cleanup() }
}
function frontend() {
  const target = path.join(desktop, 'assets', 'app')
  rmSync(target, { recursive: true, force: true })
  run(
    windows ? node : npm,
    windows ? [npm, 'run', 'build'] : ['run', 'build'],
    path.join(root, 'frontend'),
    { env: { ...env, ASTRORDER_OUT_DIR: target } },
  )
}
if (!windows && process.platform !== 'linux') throw new Error('Windows and WSL/Linux build hosts are supported.')
if (windows) {
  for (const tool of [node, npm, wails]) {
    if (!existsSync(tool)) throw new Error(`Missing local tool: ${tool}. See desktop-wails/README.md.`)
  }
}
mkdirSync(path.join(desktop, 'bin'), { recursive: true })
const output = path.join(desktop, 'bin', 'astrorder-desktop-dev.exe')
switch (action) {
  case 'install':
    run('go', ['mod', 'download'], desktop)
    runNpm(['ci', '--no-fund', '--no-audit'])
    break
  case 'bindings': bindings(); break
  case 'build':
    run(wails, ['generate', 'syso', '-arch', 'amd64', '-icon', 'assets/app.ico', '-manifest', 'assets/windows.manifest', '-out', 'rsrc_windows_amd64.syso'], desktop)
    bindings()
    frontend()
    withWailsImeFix(fix => {
      run('go', ['build', '-modfile', fix.modfile, '-trimpath', '-tags', 'production', '-ldflags', '-H=windowsgui', '-o', output, '.'], desktop, { env: fix.env })
    })
    if (windows) run(output, ['--check'], desktop)
    else console.log('Windows desktop compiled; run --check on Windows.')
    break
  case 'check':
    run('go', ['version'], desktop)
    run(wails, ['version'], desktop)
    run(node, ['--version'])
    run('go', ['mod', 'verify'], desktop)
    withWailsImeFix(fix => {
      if (windows) run('go', ['test', '-modfile', fix.modfile, './...', '-count=1'], desktop, { env: fix.env })
      else {
        run('go', ['test', '-modfile', fix.modfile, '-c', '-o', path.join(desktop, 'bin', 'astrorder-desktop.test.exe'), './...'], desktop, { env: fix.env })
        console.log('Windows desktop tests compiled; run them on Windows.')
      }
      run('go', ['vet', '-modfile', fix.modfile, './...'], desktop, { env: fix.env })
    })
    run(node, ['--test', 'assets/desktop-files.test.mjs', '../scripts/wails-ime-fix.test.mjs'], desktop)
    runNpm(['run', 'lint'])
    break
  case 'test': runNpm(['test']); break
  case 'run':
    if (!windows) throw new Error('Run the Windows desktop executable on Windows.')
    if (!existsSync(output)) throw new Error('Run build first.')
    run(output, process.argv.slice(3), desktop)
    break
  case 'smoke': {
    if (!windows) throw new Error('Run the Windows desktop smoke test on Windows.')
    if (!existsSync(output)) throw new Error('Run build first.')
    const report = path.join(desktop, 'bin', 'smoke-report.json')
    rmSync(report, { force: true })
    run(output, ['--smoke-report', report], desktop, { timeout: 30000 })
    const data = JSON.parse(readFileSync(report, 'utf8'))
    if (data.started !== true || data.bindingReady !== true) throw new Error('GUI startup or native binding verification failed')
    console.log(JSON.stringify(data))
    break
  }
  default: throw new Error('Usage: node scripts/desktop-wails.mjs install|bindings|build|check|test|run|smoke')
}
