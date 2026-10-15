import { spawnSync } from 'node:child_process'
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const desktop = path.join(root, 'desktop-wails')
const tools = path.join(root, '.tools', 'wails')
const versions = JSON.parse(readFileSync(path.join(desktop, 'toolchain.json'), 'utf8'))
const nodeDir = path.join(tools, `node-v${versions.node}-win-x64`)
const node = path.join(nodeDir, 'node.exe')
const npm = path.join(nodeDir, 'node_modules', 'npm', 'bin', 'npm-cli.js')
const wails = path.join(tools, 'bin', 'wails3.exe')
const action = process.argv[2] || 'check'
const env = { ...process.env, GOTOOLCHAIN: `go${versions.go}`, PATH: `${nodeDir}${path.delimiter}${path.join(tools, 'bin')}${path.delimiter}${process.env.PATH}`, ASTRORDER_WAILS: '1' }
function run(exe, args, cwd = root, extra = {}) {
  const result = spawnSync(exe, args, { cwd, env, stdio: 'inherit', windowsHide: true, ...extra })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${path.basename(exe)} ${args.join(' ')} exited ${result.status}`)
  return result
}
function bindings() { run(wails, ['generate', 'bindings', '-b', '-i', '-names', '-d', 'assets/bindings', '.'], desktop) }
function frontend() {
  run(node, [npm, 'run', 'build'], path.join(root, 'frontend'))
  const target = path.join(desktop, 'assets', 'app')
  rmSync(target, { recursive: true, force: true })
  cpSync(path.join(root, 'frontend', 'dist'), target, { recursive: true })
}
for (const tool of [node, npm, wails]) {
  if (!existsSync(tool)) throw new Error(`Missing local tool: ${tool}. See desktop-wails/README.md.`)
}
if (process.platform !== 'win32') throw new Error('This bootstrap currently targets Windows; add another platform deliberately.')
mkdirSync(path.join(desktop, 'bin'), { recursive: true })
const output = path.join(desktop, 'bin', 'astrorder-desktop-dev.exe')
switch (action) {
  case 'install':
    run('go', ['mod', 'download'], desktop)
    run(node, [npm, 'ci', '--no-fund', '--no-audit'], path.join(root, 'frontend'))
    break
  case 'bindings': bindings(); break
  case 'build':
    run(wails, ['generate', 'syso', '-arch', 'amd64', '-icon', 'assets/app.ico', '-manifest', 'assets/windows.manifest', '-out', 'rsrc_windows_amd64.syso'], desktop)
    bindings()
    frontend()
    run('go', ['build', '-trimpath', '-tags', 'production', '-ldflags', '-H=windowsgui', '-o', output, '.'], desktop)
    run(output, ['--check'], desktop)
    break
  case 'check':
    run('go', ['version'], desktop)
    run(wails, ['version'], desktop)
    run(node, ['--version'])
    run('go', ['mod', 'verify'], desktop)
    run('go', ['test', './...', '-count=1'], desktop)
    run('go', ['vet', './...'], desktop)
    run(node, ['--test', 'assets/desktop-files.test.mjs'], desktop)
    run(node, [npm, 'run', 'lint'], path.join(root, 'frontend'))
    break
  case 'test': run(node, [npm, 'test'], path.join(root, 'frontend')); break
  case 'run':
    if (!existsSync(output)) throw new Error('Run build first.')
    run(output, process.argv.slice(3), desktop)
    break
  case 'smoke': {
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
