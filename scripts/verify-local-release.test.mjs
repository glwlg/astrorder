import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
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
