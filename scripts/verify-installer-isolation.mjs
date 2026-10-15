import { spawn, spawnSync } from 'node:child_process'
import { existsSync, readFileSync, rmSync, statSync, mkdtempSync, mkdirSync, writeFileSync, linkSync } from 'node:fs'
import { createHash } from 'node:crypto'
import http from 'node:http'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const release = path.join(root, 'release')
const installerPath = path.join(release, 'Astrorder-Setup-0.1.0.exe')
const manifestPath = path.join(release, 'installer-manifest.json')

const scratchRoot = path.join(process.env.LOCALAPPDATA || tmpdir(), 'hermes', 'cache', 'scratch')
mkdirSync(scratchRoot, { recursive: true })
const scratch = mkdtempSync(path.join(scratchRoot, 'astrorder-installer-'))
const testInstallDir = path.join(scratch, 'install')
const isolatedStateDir = path.join(scratch, 'state')

function hashFile(filePath) {
  const content = readFileSync(filePath)
  return createHash('sha256').update(content).digest('hex')
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

function probeHttp(url, timeoutMs = 2000) {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: timeoutMs }, (res) => {
      resolve({ ok: res.statusCode >= 200 && res.statusCode < 300, status: res.statusCode })
    })
    req.on('error', () => resolve({ ok: false, status: 0 }))
    req.on('timeout', () => { req.destroy(); resolve({ ok: false, status: 0 }) })
  })
}

async function runInstaller(installer, targetDir) {
  const proc = spawnSync(installer, ['/S', '/ISOLATED', `/D=${targetDir}`], { stdio: 'ignore', windowsHide: true, timeout: 120000 })
  if (proc.error) throw proc.error
  if (proc.status !== 0) throw new Error(`Installer exit code: ${proc.status}`)
  const expectedExe = path.join(targetDir, 'Astrorder.exe')
  const expectedUninstaller = path.join(targetDir, 'uninstall.exe')
  let ready = false
  for (let i = 0; i < 40; i++) {
    await sleep(500)
    if (existsSync(expectedExe) && existsSync(expectedUninstaller)) {
      const size1 = statSync(expectedExe).size
      await sleep(500)
      const size2 = statSync(expectedExe).size
      if (size1 === size2 && size1 > 0) {
        ready = true
        break
      }
    }
  }
  if (!ready) throw new Error('安装文件解压未就绪或超时')
  return proc.status
}

async function runUninstaller(uninstaller, targetDir) {
  const proc = spawnSync(uninstaller, ['/S', `_?=${targetDir}`], { stdio: 'ignore', windowsHide: true, timeout: 120000 })
  if (proc.error) throw proc.error
  if (proc.status !== 0) throw new Error(`Uninstaller exit code: ${proc.status}`)
  const expectedExe = path.join(targetDir, 'Astrorder.exe')
  for (let i = 0; i < 30; i++) {
    await sleep(500)
    if (!existsSync(expectedExe)) break
  }
  return proc.status
}

async function main() {
  console.log('=== Astrorder Wails 3 候选安装器隔离验收 ===')

  if (!existsSync(installerPath)) {
    throw new Error(`Installer not found: ${installerPath}`)
  }
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))

  // 1. 全新安装验收
  console.log(`\n[步骤 1] 全新安装到隔离测试目录: ${testInstallDir}`)
  rmSync(testInstallDir, { recursive: true, force: true })

  const installCode = await runInstaller(installerPath, testInstallDir)
  console.log(`安装器进程执行结束，退出码: ${installCode}`)

  const installedExe = path.join(testInstallDir, 'Astrorder.exe')
  const installedHash = hashFile(installedExe)
  console.log(`安装完成: Astrorder.exe SHA256 = ${installedHash}`)
  if (!existsSync(path.join(testInstallDir, '.isolated-install'))) throw new Error('Missing isolated registration marker')
  if (hashFile(installerPath) !== manifest.sha256) throw new Error('Installer checksum mismatch')
  if (installedHash !== manifest.executableSha256) {
    throw new Error(`安装产物 SHA256 与构建清单不一致: ${installedHash} vs ${manifest.executableSha256}`)
  }

  // 验证 server/bin 下 12 个二进制文件和 SHA256SUMS
  const serverBin = path.join(testInstallDir, 'server', 'bin')
  const sha256sums = path.join(serverBin, 'SHA256SUMS')
  if (!existsSync(sha256sums)) throw new Error('缺少 server/bin/SHA256SUMS')
  const lines = readFileSync(sha256sums, 'utf8').trim().split(/\r?\n/)
  let binaryCount = 0
  for (const line of lines) {
    const parts = line.trim().split(/\s+/)
    if (parts.length < 2) continue
    const expected = parts[0]
    const rel = parts[1].replace(/^\*/, '')
    const targetFile = path.join(serverBin, rel)
    if (!existsSync(targetFile)) throw new Error(`缺少构件: ${rel}`)
    const actual = hashFile(targetFile)
    if (actual !== expected) throw new Error(`构件哈希不匹配: ${rel}`)
    binaryCount++
  }
  if (binaryCount !== manifest.baseArtifactsCount) throw new Error('Installed artifact count mismatch')
  console.log(`已成功验证安装目录中 ${binaryCount} 个关键二进制文件的逐字节完整性。`)
  for (const file of ['manifest.json', 'service-worker.js', 'session-binding.js']) {
    const source = path.join(root, 'tools', 'browser_extension', file)
    const installed = path.join(testInstallDir, 'server', 'tools', 'browser_extension', file)
    if (!existsSync(installed) || hashFile(source) !== hashFile(installed)) throw new Error(`Browser extension missing or stale: ${file}`)
  }

  // 2. 覆盖升级验收
  console.log('\n[步骤 2] 覆盖升级验收 (在已有目录重新执行安装器)...')
  // Retain a hard link so the old NTFS file identity cannot be reused.
  const oldDesktopLink = path.join(scratch, 'previous-desktop.exe')
  linkSync(installedExe, oldDesktopLink)
  const oldDesktopIdentity = statSync(oldDesktopLink, { bigint: true }).ino
  const daemonFile = path.join(serverBin, 'astrorder-sessiond.exe')
  const daemonLink = path.join(scratch, 'previous-daemon.exe')
  linkSync(daemonFile, daemonLink)
  const oldDaemonIdentity = statSync(daemonLink, { bigint: true }).ino
  const oldDaemonHash = hashFile(daemonLink)
  const upgradeCode = await runInstaller(installerPath, testInstallDir)
  console.log(`覆盖安装退出码: ${upgradeCode}`)
  await sleep(1500)
  if (!existsSync(installedExe) || hashFile(installedExe) !== manifest.executableSha256) {
    throw new Error('覆盖安装后二进制异常')
  }
  if (statSync(installedExe, { bigint: true }).ino === oldDesktopIdentity) {
    throw new Error('覆盖安装保留旧客户端文件身份，任务栏可能继续使用旧的默认图标')
  }
  rmSync(oldDesktopLink)
  if (statSync(daemonFile, { bigint: true }).ino !== oldDaemonIdentity || hashFile(daemonFile) !== oldDaemonHash) {
    throw new Error('覆盖安装修改了现有小内核')
  }
  const daemonCandidate = daemonFile + '.new'
  if (!existsSync(daemonCandidate) || hashFile(daemonCandidate) !== oldDaemonHash) {
    throw new Error('覆盖安装未正确暂存小内核候选')
  }
  rmSync(daemonLink)
  console.log('覆盖升级验证通过，客户端文件身份已更新，小内核保持原文件并暂存候选。')

  // 3. 安装包内运行时与双内核生命周期测试
  console.log('\n[步骤 3] 双内核生命周期隔离验证 (使用安装包提供的构件)...')
  const { provisionIsolatedEnvironment, stopIsolatedServices, getServiceStatus, runServiceAction } = await import('./isolated-services.mjs')
  const installedPaths = {
    root: path.join(testInstallDir, 'server'),
    stateDir: isolatedStateDir,
    dataDir: path.join(isolatedStateDir, 'data'),
    binDir: serverBin,
    ctlExecutable: path.join(serverBin, 'astrorderctl.exe'),
    appExecutable: path.join(serverBin, 'astrorder-app.exe'),
    daemonExecutable: path.join(serverBin, 'astrorder-sessiond.exe'),
  }

  await provisionIsolatedEnvironment(installedPaths)
  const configFile = path.join(isolatedStateDir, 'production.json')
  const publicConfig = JSON.parse(readFileSync(configFile, 'utf8'))
  const runtimeConfig = path.join(isolatedStateDir, 'empty-runtimes.json')
  writeFileSync(runtimeConfig, JSON.stringify({ runtimes: [] }))
  publicConfig.environment.ASTRORDER_SESSION_DAEMON_CONFIG = runtimeConfig
  publicConfig.environment.ASTRORDER_SESSION_DAEMON_DB = path.join(installedPaths.dataDir, 'session-daemon.sqlite3')
  writeFileSync(configFile, JSON.stringify(publicConfig))
  // Omit --packaged: verify the release linker default actually starts kernels.
  const shell = spawn(installedExe, ['--state-dir', isolatedStateDir], { cwd: testInstallDir, stdio: 'ignore', windowsHide: true })
  try {
    let healthy = false
    for (let i = 0; i < 60; i++) {
      if ((await probeHttp('http://127.0.0.1:30102/health')).ok) { healthy = true; break }
      await sleep(250)
    }
    if (!healthy) throw new Error('Packaged startup did not reach health readiness')

  // 查询 Daemon 状态
  const daemonStat = await getServiceStatus(installedPaths, 'daemon')
  const daemonPid = daemonStat.process?.pid
  const daemonCreationTime = daemonStat.process?.creation_time
  const daemonExe = daemonStat.process?.executable_path
  console.log(`Daemon 运行中，权威 PID: ${daemonPid}, 创建时间: ${daemonCreationTime}, 可执行文件: ${daemonExe}, 状态: ${daemonStat.state}`)
  if (!daemonPid || daemonStat.state !== 'running' || daemonStat.session_count) {
    throw new Error(`Daemon 未处于运行状态: ${JSON.stringify(daemonStat)}`)
  }

  // 验证大内核重启时小内核的生命周期独立性
  console.log('停止大内核，验证小内核独立运行...')
  await runServiceAction(installedPaths, 'app', 'stop')
  await sleep(1000)

  const probeStopped = await probeHttp('http://127.0.0.1:30102/health')
  if (probeStopped.ok) throw new Error('App 停止后端口未释放')

  const daemonStatAfterStop = await getServiceStatus(installedPaths, 'daemon')
  if (daemonStatAfterStop.process?.pid !== daemonPid ||
      daemonStatAfterStop.process?.creation_time !== daemonCreationTime ||
      daemonStatAfterStop.state !== 'running') {
    throw new Error(`Daemon 生命周期受 App 影响！原 PID: ${daemonPid}, 现 PID: ${daemonStatAfterStop.process?.pid}`)
  }
  console.log(`生命周期独立性验证成功！Daemon PID 持续保持为 ${daemonPid}，创建时间 ${daemonCreationTime}，未随 App 停止而重启。`)

  // 再次启动大内核，确认无缝重连
  console.log('重新启动大内核...')
  await runServiceAction(installedPaths, 'app', 'start')
  await sleep(2000)
  const probeReconnected = await probeHttp('http://127.0.0.1:30102/health')
  if (!probeReconnected.ok) throw new Error('大内核重新启动后未就绪')
  console.log('大内核重启就绪，健康探针 HTTP 200 OK！')

  } finally {
    shell.kill()
    if (shell.exitCode === null) await Promise.race([new Promise(resolve => shell.once('exit', resolve)), sleep(5000)])
    const cleanup = await stopIsolatedServices(installedPaths)
    if (cleanup.app?.ok !== true || cleanup.daemon?.ok !== true) throw new Error(`Isolated cleanup refused: ${JSON.stringify(cleanup)}`)
    if ((await getServiceStatus(installedPaths, 'daemon')).state !== 'stopped') throw new Error('Daemon cleanup unverified')
  }

  // 4. 静默卸载验收
  console.log(`\n[步骤 4] 静默卸载验收...`)
  const uninstallerExe = path.join(testInstallDir, 'uninstall.exe')
  const uninstCode = await runUninstaller(uninstallerExe, testInstallDir)
  console.log(`卸载器退出码: ${uninstCode}`)
  await sleep(2000)

  const exeRemaining = existsSync(installedExe)
  console.log(`卸载后 Astrorder.exe 是否存在: ${exeRemaining}`)
  if (exeRemaining) throw new Error('卸载后主程序仍残留')

  // 验证隔离用户数据目录依然安全保留
  const stateDirRemaining = existsSync(isolatedStateDir)
  console.log(`卸载后用户数据目录 (${isolatedStateDir}) 是否安全保留: ${stateDirRemaining}`)
  if (!stateDirRemaining) throw new Error('卸载过程误删了用户数据目录！')

  // 清理测试目录
  rmSync(testInstallDir, { recursive: true, force: true })

  console.log('\n=== 隔离安装与生命周期全部验收通过！===')
}

main().catch(err => {
  console.error('验收失败:', err)
  process.exit(1)
})
