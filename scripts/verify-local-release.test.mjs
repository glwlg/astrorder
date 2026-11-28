import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { stageOfficialPluginsForInstaller } from './build-installer.mjs'
import { syncOfficialPluginsVerified } from './deploy-local.mjs'
import {
  assertAppServiceStatus,
  assertDaemonServiceStatus,
  assertDesktopCheckOutput,
  assertDesktopProcess,
  assertPackagedBinaryCandidate,
  inferInstallDirFromExePath,
  selectInstallDir,
  toHostPath,
  toWindowsPath,
  verifyOfficialPluginsDir,
} from './verify-local-release.mjs'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const deployScript = path.join(scriptsDir, 'deploy-local.mjs')

function makeFakePe(t, filename) {
  const dir = mkdtempSync(path.join(tmpdir(), 'astrorder-release-gate-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const target = path.join(dir, filename)
  const buf = Buffer.alloc(128, 0)
  buf[0] = 0x4d // 'M'
  buf[1] = 0x5a // 'Z'
  writeFileSync(target, buf)
  return target
}

test('assertDesktopCheckOutput accepts packaged production stateDir and rejects dev builds', () => {
  const valid = JSON.stringify({
    framework: 'Wails v3.0.0',
    packaged: true,
    backendConfigured: false,
    stateDir: 'C:\\Users\\luwei\\AppData\\Local\\Astrorder',
    rootDir: 'P:\\DevApp\\Astrorder\\server',
    kernelManagement: true,
  })
  const parsed = assertDesktopCheckOutput(valid, {
    expectedStateDir: 'C:\\Users\\luwei\\AppData\\Local\\Astrorder',
  })
  assert.equal(parsed.packaged, true)

  const devStateDir = JSON.stringify({
    framework: 'Wails v3.0.0',
    packaged: false,
    backendConfigured: false,
    stateDir: 'C:\\Users\\luwei\\AppData\\Local\\Astrorder-Wails-Dev',
    kernelManagement: true,
  })
  assert.throws(() => assertDesktopCheckOutput(devStateDir), /DEV mode/i)

  const devFlag = JSON.stringify({
    framework: 'Wails v3.0.0',
    packaged: false,
    stateDir: 'C:\\Users\\luwei\\AppData\\Local\\Astrorder',
    kernelManagement: true,
  })
  assert.throws(() => assertDesktopCheckOutput(devFlag), /packaged=false/)

  assert.throws(() => assertDesktopCheckOutput(''), /empty output/)
  assert.throws(
    () =>
      assertDesktopCheckOutput(
        JSON.stringify({
          packaged: true,
          stateDir: 'C:\\Users\\luwei\\AppData\\Local\\Astrorder',
          kernelManagement: false,
        }),
      ),
    /kernelManagement=false/,
  )
})

test('assertPackagedBinaryCandidate rejects dev executable names and non-PE files', t => {
  const devExe = makeFakePe(t, 'astrorder-desktop-dev.exe')
  assert.throws(() => assertPackagedBinaryCandidate(devExe), /Refusing development desktop binary/)

  const dir = mkdtempSync(path.join(tmpdir(), 'astrorder-nonpe-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const badExe = path.join(dir, 'Astrorder.exe')
  writeFileSync(badExe, 'not-a-pe-executable-content-that-is-long-enough-to-pass-size-check-1234567890')
  assert.throws(() => assertPackagedBinaryCandidate(badExe), /PE \(MZ\) header/)

  const validExe = makeFakePe(t, 'Astrorder.exe')
  const info = assertPackagedBinaryCandidate(validExe)
  assert.equal(info.sha256.length, 64)
})

test('assertAppServiceStatus validates running, healthy, verified app service and path', () => {
  const good = JSON.stringify({
    ok: true,
    state: 'running',
    healthy: true,
    identity_verified: true,
    port_owned: true,
    address: '127.0.0.1:30001',
    process: { pid: 101004, executable_path: 'P:\\DevApp\\Astrorder\\server\\bin\\astrorder-app.exe' },
  })
  const parsed = assertAppServiceStatus(good, {
    expectedExecutable: 'p:/DevApp/Astrorder/server/bin/astrorder-app.exe',
  })
  assert.equal(parsed.process.pid, 101004)

  assert.throws(
    () =>
      assertAppServiceStatus(
        JSON.stringify({ ok: true, state: 'running', healthy: false, identity_verified: true, port_owned: true }),
      ),
    /health check failed/,
  )
  assert.throws(
    () =>
      assertAppServiceStatus(good, {
        expectedExecutable: 'C:\\Other\\astrorder-app.exe',
      }),
    /unexpected path/,
  )
})

test('assertDaemonServiceStatus detects unexpected daemon PID changes', () => {
  const good = JSON.stringify({
    ok: true,
    state: 'running',
    identity_verified: true,
    port_owned: true,
    process: { pid: 83868 },
  })
  assert.equal(assertDaemonServiceStatus(good, { expectedPid: 83868 }).process.pid, 83868)
  assert.throws(() => assertDaemonServiceStatus(good, { expectedPid: 99999 }), /PID changed/)
})

test('assertDesktopProcess requires interactive session, matching path, and visible main window', () => {
  const expected = 'P:\\DevApp\\Astrorder\\Astrorder.exe'
  assert.throws(() => assertDesktopProcess([], { expectedExePath: expected }), /not running/)
  assert.throws(
    () =>
      assertDesktopProcess(
        [{ Id: 1, SessionId: 0, MainWindowTitle: '星序', Path: expected }],
        { expectedExePath: expected },
      ),
    /interactive desktop session/,
  )
  assert.throws(
    () =>
      assertDesktopProcess(
        [{ Id: 2, SessionId: 1, MainWindowTitle: '', Path: expected }],
        { expectedExePath: expected },
      ),
    /MainWindowTitle/,
  )
  const proc = assertDesktopProcess(
    [{ Id: 3, SessionId: 1, MainWindowTitle: '星序', Path: 'p:\\devapp\\astrorder\\Astrorder.exe' }],
    { expectedExePath: expected },
  )
  assert.equal(proc.Id, 3)
})

test('WSL and Windows paths convert cleanly both ways', () => {
  assert.equal(toWindowsPath('/mnt/p/DevApp/Astrorder/Astrorder.exe'), 'P:\\DevApp\\Astrorder\\Astrorder.exe')
  assert.equal(toHostPath('P:\\DevApp\\Astrorder\\Astrorder.exe', 'linux'), '/mnt/p/DevApp/Astrorder/Astrorder.exe')
  assert.equal(toHostPath('P:\\DevApp\\Astrorder\\Astrorder.exe', 'win32'), 'P:\\DevApp\\Astrorder\\Astrorder.exe')
})

test('selectInstallDir prefers live running installation, rejects mismatches, and never silently falls back to default Programs dir', () => {
  assert.equal(
    inferInstallDirFromExePath('P:\\DevApp\\Astrorder\\server\\bin\\astrorder-sessiond.exe'),
    'P:\\DevApp\\Astrorder',
  )
  assert.equal(
    selectInstallDir({
      liveExePaths: [
        'P:\\DevApp\\Astrorder\\server\\bin\\astrorder-sessiond.exe',
        'P:\\DevApp\\Astrorder\\server\\bin\\astrorder-app.exe',
        'P:\\DevApp\\Astrorder\\Astrorder.exe',
      ],
      registryExePath: 'C:\\Users\\luwei\\AppData\\Local\\Programs\\Astrorder\\Astrorder.exe',
    }),
    'P:\\DevApp\\Astrorder',
  )
  assert.throws(
    () =>
      selectInstallDir({
        override: 'C:\\Users\\luwei\\AppData\\Local\\Programs\\Astrorder',
        liveExePaths: ['P:\\DevApp\\Astrorder\\server\\bin\\astrorder-sessiond.exe'],
      }),
    /Refusing deploy\/verify/,
  )
  assert.throws(
    () =>
      selectInstallDir({
        liveExePaths: [
          'P:\\DevApp\\Astrorder\\Astrorder.exe',
          'C:\\Users\\luwei\\AppData\\Local\\Programs\\Astrorder\\Astrorder.exe',
        ],
      }),
    /Multiple live Astrorder installations/,
  )
  assert.throws(() => selectInstallDir({}), /Unable to determine installed Astrorder directory/)
})

test('deploy-local.mjs --dry-run blocks development desktop binaries and accepts packaged candidates', t => {
  const devExe = makeFakePe(t, 'astrorder-desktop-dev.exe')
  const fail = spawnSync(process.execPath, [deployScript, '--desktop', '--desktop-exe', devExe, '--dry-run'], {
    encoding: 'utf8',
  })
  assert.notEqual(fail.status, 0)
  assert.match(fail.stderr, /Refusing development desktop binary/)

  const packagedExe = makeFakePe(t, 'Astrorder.exe')
  const ok = spawnSync(process.execPath, [deployScript, '--desktop', '--desktop-exe', packagedExe, '--dry-run'], {
    encoding: 'utf8',
  })
  assert.equal(ok.status, 0, ok.stderr)
  assert.equal(JSON.parse(ok.stdout).ok, true)
})

function writeMockOfficialPlugin(rootDir, id, { entryCode, version = '1.0.0', css = '.box{color:red}' } = {}) {
  const pluginDir = path.join(rootDir, id)
  mkdirSync(path.join(pluginDir, 'ui'), { recursive: true })
  const manifest = {
    schema_version: 1,
    id,
    name: `Plugin ${id}`,
    version,
    backend: { type: 'none' },
    settings: { mode: 'none' },
    frontend: {
      mode: 'esm',
      entry: 'ui/entry.js',
      styles: css ? ['ui/entry.css'] : [],
      sdk_version: '^1.0',
    },
    contributes: {},
  }
  writeFileSync(path.join(pluginDir, 'plugin.json'), JSON.stringify(manifest, null, 2))
  writeFileSync(
    path.join(pluginDir, 'ui', 'entry.js'),
    entryCode ?? `import { defineAstrorderPlugin } from '@astrorder/sdk'\nexport function activate() {}\nexport default defineAstrorderPlugin({ activate })\n`,
  )
  if (css) {
    writeFileSync(path.join(pluginDir, 'ui', 'entry.css'), css)
  }
  return pluginDir
}

test('verifyOfficialPluginsDir validates matching built plugins and detects missing, stale, SHA mismatch, and forbidden imports', t => {
  const base = mkdtempSync(path.join(tmpdir(), 'astrorder-official-plugins-'))
  t.after(() => rmSync(base, { recursive: true, force: true }))

  const expectedDist = path.join(base, 'expected')
  const installedDir = path.join(base, 'installed')
  mkdirSync(expectedDist, { recursive: true })
  mkdirSync(installedDir, { recursive: true })

  writeMockOfficialPlugin(expectedDist, 'alpha-viewer')
  writeMockOfficialPlugin(expectedDist, 'beta-viewer')
  writeMockOfficialPlugin(installedDir, 'alpha-viewer')
  writeMockOfficialPlugin(installedDir, 'beta-viewer')

  // 1. Pass: exact plugin IDs & matching entry SHA-256
  const report = verifyOfficialPluginsDir(installedDir, expectedDist)
  assert.equal(report.count, 2)
  assert.deepEqual(
    report.plugins.map((p) => p.id),
    ['alpha-viewer', 'beta-viewer'],
  )

  // 2. Missing plugin
  const missingDir = path.join(base, 'installed-missing')
  mkdirSync(missingDir, { recursive: true })
  writeMockOfficialPlugin(missingDir, 'alpha-viewer')
  assert.throws(() => verifyOfficialPluginsDir(missingDir, expectedDist), /missing from installed directory/)

  // 3. Stale extra plugin
  const staleDir = path.join(base, 'installed-stale')
  mkdirSync(staleDir, { recursive: true })
  writeMockOfficialPlugin(staleDir, 'alpha-viewer')
  writeMockOfficialPlugin(staleDir, 'beta-viewer')
  writeMockOfficialPlugin(staleDir, 'legacy-stale-viewer')
  assert.throws(() => verifyOfficialPluginsDir(staleDir, expectedDist), /Unexpected stale official plugin/)

  // 4. SHA-256 mismatch
  const mismatchDir = path.join(base, 'installed-mismatch')
  mkdirSync(mismatchDir, { recursive: true })
  writeMockOfficialPlugin(mismatchDir, 'alpha-viewer')
  writeMockOfficialPlugin(mismatchDir, 'beta-viewer', {
    entryCode: `import { defineAstrorderPlugin } from '@astrorder/sdk'\nexport function activate() { return 'tampered' }\nexport default defineAstrorderPlugin({ activate })\n`,
  })
  assert.throws(() => verifyOfficialPluginsDir(mismatchDir, expectedDist), /SHA-256 mismatch/)

  // 5. Forbidden unmapped external import
  const badImportDir = path.join(base, 'installed-bad-import')
  mkdirSync(badImportDir, { recursive: true })
  writeMockOfficialPlugin(badImportDir, 'alpha-viewer', {
    entryCode: `import _ from 'lodash'\nexport function activate() { return _ }\n`,
  })
  assert.throws(() => verifyOfficialPluginsDir(badImportDir), /unmapped external bare import "lodash"/)
})

test('syncOfficialPluginsVerified removes stale official plugins while preserving external plugins and supports --plugins --dry-run', t => {
  const base = mkdtempSync(path.join(tmpdir(), 'astrorder-sync-plugins-'))
  t.after(() => rmSync(base, { recursive: true, force: true }))

  const srcDist = path.join(base, 'official-dist')
  const installDir = path.join(base, 'install')
  mkdirSync(srcDist, { recursive: true })
  writeMockOfficialPlugin(srcDist, 'alpha-viewer')
  writeMockOfficialPlugin(srcDist, 'beta-viewer')

  // Pre-seed stale official plugin AND user external plugin
  const preOfficial = path.join(installDir, 'server', 'plugins', 'official')
  const externalPluginFile = path.join(installDir, 'server', 'plugins', 'external', 'custom-user-plugin', 'plugin.json')
  mkdirSync(path.dirname(externalPluginFile), { recursive: true })
  writeFileSync(externalPluginFile, '{"id":"custom-user-plugin"}')
  writeMockOfficialPlugin(preOfficial, 'stale-removed-plugin')

  const synced = syncOfficialPluginsVerified(srcDist, installDir)
  assert.equal(synced.count, 2)
  assert.equal(existsSync(path.join(preOfficial, 'stale-removed-plugin')), false)
  assert.equal(existsSync(path.join(preOfficial, 'alpha-viewer', 'plugin.json')), true)
  assert.equal(existsSync(path.join(preOfficial, 'beta-viewer', 'plugin.json')), true)
  assert.equal(readFileSync(externalPluginFile, 'utf8'), '{"id":"custom-user-plugin"}')

  // Dry-run CLI validation with --plugins
  const dryOk = spawnSync(
    process.execPath,
    [deployScript, '--plugins', '--official-plugins-dist', srcDist, '--dry-run'],
    { encoding: 'utf8' },
  )
  assert.equal(dryOk.status, 0, dryOk.stderr)
  const dryPayload = JSON.parse(dryOk.stdout)
  assert.equal(dryPayload.ok, true)
  assert.deepEqual(dryPayload.components, ['plugins'])
  assert.equal(dryPayload.officialPluginsCount, 2)
})

test('stageOfficialPluginsForInstaller stages and SHA-verifies official plugins into installer build-package', t => {
  const base = mkdtempSync(path.join(tmpdir(), 'astrorder-installer-plugins-'))
  t.after(() => rmSync(base, { recursive: true, force: true }))

  const srcDist = path.join(base, 'official-dist')
  const pkgDir = path.join(base, 'build-package')
  mkdirSync(srcDist, { recursive: true })
  mkdirSync(pkgDir, { recursive: true })

  writeMockOfficialPlugin(srcDist, 'workspace-digest')
  writeMockOfficialPlugin(srcDist, 'office-viewer')
  // Pre-seed stale directory in build-package to confirm clean replacement
  writeMockOfficialPlugin(path.join(pkgDir, 'server', 'plugins', 'official'), 'stale-old-plugin')

  const staged = stageOfficialPluginsForInstaller(srcDist, pkgDir)
  assert.equal(staged.count, 2)
  assert.deepEqual(
    staged.plugins.map((p) => p.id),
    ['office-viewer', 'workspace-digest'],
  )
  assert.equal(existsSync(path.join(pkgDir, 'server', 'plugins', 'official', 'stale-old-plugin')), false)
  assert.equal(existsSync(path.join(pkgDir, 'server', 'plugins', 'official', 'workspace-digest', 'ui', 'entry.js')), true)
  assert.equal(existsSync(path.join(pkgDir, 'server', 'plugins', 'official', 'office-viewer', 'plugin.json')), true)

  // Missing source dist must fail loudly
  assert.throws(
    () => stageOfficialPluginsForInstaller(path.join(base, 'does-not-exist'), pkgDir),
    /does not exist/,
  )
})
