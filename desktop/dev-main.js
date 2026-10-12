const { app, BrowserWindow, dialog, session, shell } = require('electron')
const { execFile } = require('node:child_process')
const { readFileSync, existsSync } = require('node:fs')
const path = require('node:path')
const { promisify } = require('node:util')
const execute = promisify(execFile)
const root = path.resolve(__dirname, '..')
const stateDir = process.env.ASTRORDER_DEV_STATE_DIR || path.join(process.env.LOCALAPPDATA || app.getPath('appData'), 'Astrorder-Go-Dev')
const binDir = process.env.ASTRORDER_DEV_BIN_DIR || path.join(root, '.runtime', 'dev-bin')
const ctl = path.join(binDir, process.platform === 'win32' ? 'astrorderctl.exe' : 'astrorderctl')
app.setName('Astrorder Go Dev')
app.setPath('userData', path.join(stateDir, 'desktop'))
let window

async function control(...args) {
  const { stdout } = await execute(ctl, args, { cwd: root, windowsHide: true, timeout: 60000 })
  return stdout.trim()
}

if (!app.requestSingleInstanceLock()) app.quit()
else {
  app.on('second-instance', () => { if (window) { window.show(); window.focus() } })
  app.whenReady().then(async () => {
    if (!existsSync(ctl)) throw new Error('请先构建 .runtime/dev-bin 中的 Go 开发版可执行文件。')
    const production = JSON.parse(readFileSync(path.join(stateDir, 'production.json'), 'utf8'))
    const port = Number(production.environment?.ASTRORDER_PORT)
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('开发版 HTTP 端口配置无效。')
    const base = `http://127.0.0.1:${port}`
    for (const target of ['daemon', 'app']) {
      const result = JSON.parse(await control('service', target, 'start', '--root', root, '--state-dir', stateDir))
      if (!result.ok || result.state !== 'running') throw new Error(result.message || `${target} 启动失败`)
    }
    const token = await control('login', '--state-dir', stateDir)
    if (!token) throw new Error('开发版访问凭证不可用。')
    await session.defaultSession.cookies.set({ url: base, name: 'astrorder_session', value: token, httpOnly: true, sameSite: 'strict', path: '/' })
    window = new BrowserWindow({
      width: 1440, height: 960, minWidth: 960, minHeight: 640,
      title: '星序 · Go 开发版',
      icon: path.join(root, 'frontend', 'public', 'favicon.ico'),
      webPreferences: { contextIsolation: true, sandbox: true, nodeIntegration: false },
    })
    window.removeMenu()
    window.webContents.on('page-title-updated', event => { event.preventDefault(); window.setTitle('星序 · Go 开发版') })
    window.webContents.setWindowOpenHandler(({ url }) => { if (/^https?:/.test(url)) void shell.openExternal(url); return { action: 'deny' } })
    window.webContents.on('will-navigate', (event, url) => { if (new URL(url).origin !== base) event.preventDefault() })
    await window.loadURL(`${base}/chat`)
    console.log(JSON.stringify({ ready: true, url: base, dev: true }))
  }).catch(error => { console.error(error.message); dialog.showErrorBox('星序 Go 开发版启动失败', error.message); app.exit(1) })
  app.on('window-all-closed', () => app.quit())
}
