import { spawnSync } from 'node:child_process'
import { cpSync, createReadStream, existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const desktop = path.join(root, 'desktop-wails')
const release = path.join(root, 'release')
const node = path.join(root, '.tools', 'wails', 'node-v26.11.1-win-x64', 'node.exe')
const npm = path.join(root, '.tools', 'wails', 'node-v26.11.1-win-x64', 'node_modules', 'npm', 'bin', 'npm-cli.js')
const wails = path.join(root, '.tools', 'wails', 'bin', 'wails3.exe')
const makensis = path.join(root, '.tools', 'nsis', 'makensis.exe')
const goStage = path.join(root, 'desktop', '.runtime', 'go-stage')
const versions = JSON.parse(readFileSync(path.join(desktop, 'toolchain.json'), 'utf8'))
// Same environment as scripts/desktop-wails.mjs: pinned Go toolchain and the
// Wails frontend build flag (disables the PWA service worker inside WebView2).
const env = { ...process.env, GOTOOLCHAIN: `go${versions.go}`, ASTRORDER_WAILS: '1' }

function hashFile(filePath) {
  const content = readFileSync(filePath)
  return createHash('sha256').update(content).digest('hex')
}

function run(cmd, args, cwd) {
  console.log(`> [${cwd || process.cwd()}] ${cmd} ${args.join(' ')}`)
  const result = spawnSync(cmd, args, { cwd: cwd || root, env, stdio: 'inherit', shell: false, windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(`Command failed with exit code ${result.status}: ${cmd} ${args.join(' ')}`)
  }
}

async function verifyStage(stageDir) {
  const manifestPath = path.join(stageDir, 'SHA256SUMS')
  if (!existsSync(manifestPath)) throw new Error('Missing SHA256SUMS in stage')
  const manifest = readFileSync(manifestPath, 'utf8')
  const lines = manifest.trim().split(/\r?\n/)
  const checked = []
  for (const line of lines) {
    const parts = line.trim().split(/\s+/)
    if (parts.length < 2) continue
    const expectedHash = parts[0]
    const relativeFile = parts[1].replace(/^\*/, '')
    const fullPath = path.join(stageDir, relativeFile)
    if (!existsSync(fullPath)) throw new Error(`Stage artifact missing: ${relativeFile}`)
    const actualHash = hashFile(fullPath)
    if (actualHash !== expectedHash) {
      throw new Error(`Checksum mismatch for ${relativeFile}: expected ${expectedHash}, got ${actualHash}`)
    }
    checked.push({ file: relativeFile, hash: actualHash, size: statSync(fullPath).size })
  }
  return checked
}

async function main() {
  console.log('=== Astrorder Wails 3 候选安装器构建流程 ===')

  if (!existsSync(makensis)) {
    throw new Error(`makensis.exe not found at ${makensis}. Run scripts/install-nsis.ps1 first.`)
  }
  // Rebuild the canonical kernel artifacts before packaging source changes.
  console.log('\n[1/7] 构建并验证 Go 阶段构件...')
  run(node, ['desktop/stage-go-artifacts.js'], root)
  const verifiedArtifacts = await verifyStage(goStage)
  console.log(`已成功验证 ${verifiedArtifacts.length} 个基础构件及其 SHA256。`)

  // 2. 生成 Wails bindings
  console.log('\n[2/7] 生成 Wails 原生绑定...')
  // Must match scripts/desktop-wails.mjs: the shell embeds assets/bindings.
  run(wails, ['generate', 'bindings', '-b', '-i', '-names', '-d', 'assets/bindings', '.'], desktop)

  // 3. 构建前端
  console.log('\n[3/7] 构建前端 React SPA...')
  run(node, [npm, 'run', 'build'], path.join(root, 'frontend'))
  const embeddedApp = path.join(desktop, 'assets', 'app')
  rmSync(embeddedApp, { recursive: true, force: true })
  cpSync(path.join(root, 'frontend', 'dist'), embeddedApp, { recursive: true })

  // 4. 编译 Wails 正式版 Astrorder.exe
  console.log('\n[4/7] 编译正式版 Wails 桌面客户端 Astrorder.exe...')
  const pkgDir = path.join(desktop, 'build-package')
  rmSync(pkgDir, { recursive: true, force: true })
  mkdirSync(pkgDir, { recursive: true })
  const astrorderExe = path.join(pkgDir, 'Astrorder.exe')

  run(wails, ['generate', 'syso', '-arch', 'amd64', '-icon', 'assets/app.ico', '-manifest', 'assets/windows.manifest', '-out', 'rsrc_windows_amd64.syso'], desktop)

  run('go', [
    'build',
    '-trimpath',
    '-tags', 'production',
    '-ldflags', '-H=windowsgui -s -w -X main.defaultPackaged=true',
    '-o', astrorderExe,
    '.'
  ], desktop)

  if (!existsSync(astrorderExe)) throw new Error('Astrorder.exe build failed')
  const exeHash = hashFile(astrorderExe)
  console.log(`Astrorder.exe 编译成功，大小: ${(statSync(astrorderExe).size / 1024 / 1024).toFixed(2)} MB, SHA256: ${exeHash}`)

  // 5. 编排打包目录 build-package
  console.log('\n[5/7] 编排安装器文件树 (server/bin, connectors, plugins, dist)...')

  // 5.1 server/bin
  const serverBin = path.join(pkgDir, 'server', 'bin')
  mkdirSync(serverBin, { recursive: true })
  cpSync(goStage, serverBin, { recursive: true })

  // 5.2 connectors/codex/codex-plugin
  const codexDest = path.join(pkgDir, 'server', 'connectors', 'codex', 'codex-plugin')
  mkdirSync(path.dirname(codexDest), { recursive: true })
  cpSync(path.join(root, 'connectors', 'codex', 'codex-plugin'), codexDest, { recursive: true })

  // 5.3 .hermes/plugins/astrorder-hermes
  const hermesPluginDest = path.join(pkgDir, 'server', '.hermes', 'plugins', 'astrorder-hermes')
  mkdirSync(path.dirname(hermesPluginDest), { recursive: true })
  cpSync(path.join(root, '.hermes', 'plugins', 'astrorder-hermes'), hermesPluginDest, { recursive: true })

  // 5.4 connectors/hermes/astrorder_hermes_plugin
  const hermesConnDest = path.join(pkgDir, 'server', 'connectors', 'hermes', 'astrorder_hermes_plugin')
  mkdirSync(path.dirname(hermesConnDest), { recursive: true })
  cpSync(path.join(root, 'connectors', 'hermes', 'astrorder_hermes_plugin'), hermesConnDest, { recursive: true })

  // The shell keeps its embedded Wails build; browser/PWA assets need a
  // separate build without ASTRORDER_WAILS before entering the installer.
  const browserEnv = { ...env }
  delete browserEnv.ASTRORDER_WAILS
  const browserBuild = spawnSync(node, [npm, 'run', 'build'], {
    cwd: path.join(root, 'frontend'), env: browserEnv,
    stdio: 'inherit', shell: false, windowsHide: true,
  })
  if (browserBuild.error) throw browserBuild.error
  if (browserBuild.status !== 0) throw new Error(`Browser build failed: ${browserBuild.status}`)
  if (!existsSync(path.join(root, 'frontend', 'dist', 'sw-astrorder.js'))) throw new Error('Browser PWA worker missing')
  if (existsSync(path.join(embeddedApp, 'sw-astrorder.js'))) throw new Error('Desktop embeds a PWA worker')

  // 5.5 frontend/dist
  const frontDist = path.join(pkgDir, 'server', 'frontend', 'dist')
  mkdirSync(path.dirname(frontDist), { recursive: true })
  cpSync(path.join(root, 'frontend', 'dist'), frontDist, { recursive: true })

  // 5.6 favicon.ico
  const favDest = path.join(pkgDir, 'server', 'frontend', 'public', 'favicon.ico')
  mkdirSync(path.dirname(favDest), { recursive: true })
  cpSync(path.join(root, 'frontend', 'public', 'favicon.ico'), favDest)

  cpSync(path.join(root, 'tools', 'browser_extension'), path.join(pkgDir, 'server', 'tools', 'browser_extension'), {
    recursive: true, filter: source => !source.endsWith('.test.mjs'),
  })

  // 再次核对 staged 出来的 server/bin SHA256 确保无损
  await verifyStage(serverBin)

  // 6. 调用 NSIS 编译打包
  console.log('\n[6/7] 执行 NSIS 编译安装器...')
  mkdirSync(release, { recursive: true })
  const nsiScript = path.join(desktop, 'installer.nsi')
  run(makensis, ['/INPUTCHARSET', 'UTF8', nsiScript], desktop)

  const installerExe = path.join(release, 'Astrorder-Setup-0.1.0.exe')
  if (!existsSync(installerExe)) throw new Error('Installer generation failed')

  // 7. 计算安装器 SHA256 和大小
  console.log('\n[7/7] 产出验证与摘要统计...')
  const installerHash = hashFile(installerExe)
  const installerSize = statSync(installerExe).size

  // 清理临时打包目录
  rmSync(pkgDir, { recursive: true, force: true })

  const summary = {
    installerPath: installerExe,
    fileSize: installerSize,
    fileSizeMB: (installerSize / (1024 * 1024)).toFixed(2) + ' MB',
    sha256: installerHash,
    executableSha256: exeHash,
    baseArtifactsCount: verifiedArtifacts.length,
    timestamp: new Date().toISOString()
  }

  const manifestOutput = path.join(release, 'installer-manifest.json')
  writeFileSync(manifestOutput, JSON.stringify(summary, null, 2))

  console.log('\n=== 安装器构建完成 ===')
  console.log(JSON.stringify(summary, null, 2))
}

main().catch(err => {
  console.error('Build installer error:', err)
  process.exit(1)
})
