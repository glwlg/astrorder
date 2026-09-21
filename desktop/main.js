const { app, BrowserWindow, Menu, Tray, clipboard, dialog, ipcMain, nativeImage, net, session, shell, Notification } = require('electron')
const { execFile } = require('node:child_process')
const { existsSync, readFileSync, writeFileSync } = require('node:fs')
const path = require('node:path')
const { pathToFileURL } = require('node:url')

let root
let python
let serviceScript
let tokenScript
let appUrl
const statusFile = path.join(__dirname, 'service-status.html')
const statusUrl = pathToFileURL(statusFile).href
const splashFile = path.join(__dirname, 'splash.html')
const splashUrl = pathToFileURL(splashFile).href
let window
let tray
let quitting = false
let busy = false
let states = { app: { state: 'unknown' }, daemon: { state: 'unknown' } }
let previewWindow = null

function previewOrigin() {
  try { return new URL(appUrl).origin } catch { return '' }
}

function allowedPreviewUrl(url) {
  if (typeof url !== 'string' || !url) return false
  if (url.startsWith('data:image/')) return true
  try {
    const parsed = new URL(url)
    return parsed.origin === previewOrigin() && parsed.protocol === 'http:'
  } catch {
    return false
  }
}

function fetchPreviewImage(url) {
  if (url.startsWith('data:')) return Promise.resolve(url)
  if (!allowedPreviewUrl(url)) return Promise.reject(new Error('invalid image url'))
  return new Promise((resolve, reject) => {
    const request = net.request({
      url,
      session: window.webContents.session,
      credentials: 'include',
    })
    const chunks = []
    request.on('response', response => {
      if (response.statusCode !== 200) {
        reject(new Error('image http ' + response.statusCode))
        return
      }
      const type = String(response.headers['content-type'] || 'image/png').split(';')[0]
      if (!type.startsWith('image/')) {
        reject(new Error('not an image'))
        return
      }
      response.on('data', chunk => chunks.push(chunk))
      response.on('end', () => {
        resolve(`data:${type};base64,${Buffer.concat(chunks).toString('base64')}`)
      })
    })
    request.on('error', reject)
    request.end()
  })
}

function placePreview() {
  if (!previewWindow || previewWindow.isDestroyed() || !window || window.isDestroyed()) return
  previewWindow.setBounds(window.getBounds())
  previewWindow.moveTop()
}

function closePreview() {
  if (window && !window.isDestroyed()) {
    window.removeListener('move', placePreview)
    window.removeListener('resize', placePreview)
    window.removeListener('focus', placePreview)
    try { window.setEnabled(true) } catch {}
  }
  if (previewWindow && !previewWindow.isDestroyed()) previewWindow.close()
  previewWindow = null
  if (window && !window.isDestroyed()) window.focus()
}

function openPreview(payload) {
  if (!window || window.isDestroyed() || !payload || !Array.isArray(payload.images)) return
  const images = payload.images.filter(allowedPreviewUrl)
  if (!images.length) return
  const data = {
    images,
    index: Math.max(0, Math.min(Number(payload.index) || 0, images.length - 1)),
  }
  if (previewWindow && !previewWindow.isDestroyed()) {
    placePreview()
    previewWindow.webContents.send('preview:data', data)
    previewWindow.focus()
    return
  }
  const bounds = window.getBounds()
  previewWindow = new BrowserWindow({
    frame: false,
    show: false,
    x: bounds.x,
    y: bounds.y,
    width: bounds.width,
    height: bounds.height,
    backgroundColor: '#111111',
    skipTaskbar: true,
    resizable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    autoHideMenuBar: true,
    thickFrame: false,
    webPreferences: {
      preload: path.join(__dirname, 'preview-preload.js'),
      contextIsolation: true,
      sandbox: true,
    },
  })
  window.on('move', placePreview)
  window.on('resize', placePreview)
  window.on('focus', placePreview)
  previewWindow.on('closed', () => {
    window.removeListener('move', placePreview)
    window.removeListener('resize', placePreview)
    window.removeListener('focus', placePreview)
    if (window && !window.isDestroyed()) {
      try { window.setEnabled(true) } catch {}
    }
    previewWindow = null
  })
  previewWindow.webContents.on('did-finish-load', () => {
    if (previewWindow && !previewWindow.isDestroyed()) {
      previewWindow.webContents.send('preview:data', data)
    }
  })
  previewWindow.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  previewWindow.webContents.on('will-navigate', event => event.preventDefault())
  previewWindow.once('ready-to-show', () => {
    placePreview()
    if (previewWindow && !previewWindow.isDestroyed()) {
      try { window.setEnabled(false) } catch {}
      previewWindow.show()
      previewWindow.moveTop()
      previewWindow.focus()
    }
  })
  previewWindow.webContents.on('before-input-event', (event, input) => {
    if (input.type === 'keyDown' && input.key === 'Escape') {
      event.preventDefault()
      closePreview()
    }
  })
  void previewWindow.loadFile(path.join(__dirname, 'image-preview.html'))
}

function getStoredTheme() {
  try {
    const prefFile = path.join(root, '.runtime', 'preferences.json')
    if (existsSync(prefFile)) {
      const data = JSON.parse(readFileSync(prefFile, 'utf8'))
      if (data?.appearance?.theme) return data.appearance.theme
    }
  } catch {}
  return 'light'
}

async function configureRoot() {
  const developmentRoot = path.resolve(__dirname, '..')
  const candidate = app.isPackaged ? path.join(path.dirname(process.execPath), 'server') : developmentRoot
  const valid = value => typeof value === 'string'
    && existsSync(path.join(value, 'scripts', 'desktop_service.py'))
    && existsSync(path.join(value, 'backend', '.venv', 'Scripts', 'python.exe'))
  if (!valid(candidate)) {
    throw new Error('安装目录中的服务端文件不完整，请重新安装星序')
  }
  root = candidate
  python = path.join(root, 'backend', '.venv', 'Scripts', 'python.exe')
  serviceScript = path.join(root, 'scripts', 'desktop_service.py')
  tokenScript = path.join(root, 'scripts', 'print_browser_token.py')
  if (app.isPackaged) {
    await new Promise((resolve, reject) => {
      execFile(python, [path.join(root, 'scripts', 'provision_desktop.py')], {
        cwd: root, windowsHide: true,
      }, error => error ? reject(new Error('初始化服务端配置失败')) : resolve())
    })
  }
  const production = JSON.parse(readFileSync(path.join(process.env.LOCALAPPDATA, 'Astrorder', 'production.json'), 'utf8'))
  const port = Number(production.environment?.ASTRORDER_PORT || 30001)
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('生产端口配置无效')
  appUrl = `http://127.0.0.1:${port}`
}

function service(target, action, confirmActive = false) {
  return new Promise((resolve, reject) => {
    const args = [serviceScript, target, action]
    if (confirmActive) args.push('--confirm-active')
    execFile(python, args, {
      cwd: root,
      windowsHide: true,
      timeout: 45000,
      env: { ...process.env, ASTRORDER_DESKTOP_PID: String(process.pid) },
    }, (error, stdout) => {
      let result
      try {
        result = JSON.parse(stdout.trim())
      } catch {
        reject(new Error('服务管理返回了无效结果'))
        return
      }
      if (error || !result.ok) reject(new Error(result.message || '服务操作失败'))
      else resolve(result)
    })
  })
}

function label(state) {
  return ({
    running: '运行中', stopped: '已停止', unhealthy: '异常', conflict: '端口冲突',
    unknown: '状态未知', starting: '启动中', stopping: '停止中', restarting: '重启中',
  })[state] || '状态未知'
}

async function refresh() {
  if (busy) return
  const [appState, daemonState] = await Promise.allSettled([
    service('app', 'status'), service('daemon', 'status'),
  ])
  states.app = appState.status === 'fulfilled' ? appState.value : { state: 'unknown' }
  states.daemon = daemonState.status === 'fulfilled' ? daemonState.value : { state: 'unknown' }
  rebuildMenu()
}

async function run(target, action) {
  if (busy) return
  let confirmActive = false
  if (target === 'daemon' && (action === 'stop' || action === 'restart')) {
    const active = states.daemon.active_sessions || {}
    if (Object.keys(active).length || states.daemon.state !== 'running') {
      const detail = Object.keys(active).length
        ? `当前有 ${Object.keys(active).length} 个运行中或等待审批的会话。`
        : '无法确认小内核中的会话状态。'
      const answer = await dialog.showMessageBox(window, {
        type: 'warning',
        title: '确认操作小内核',
        message: action === 'restart' ? '确认重启小内核？' : '确认停止小内核？',
        detail: `${detail} 此操作可能中断任务。`,
        buttons: ['取消', action === 'restart' ? '中断会话并重启' : '中断会话并停止'],
        defaultId: 0,
        cancelId: 0,
      })
      if (answer.response !== 1) return
      confirmActive = true
    }
  }
  busy = true
  states[target] = { ...states[target], state: `${action}ing`.replace('stoping', 'stopping') }
  rebuildMenu()
  try {
    states[target] = await service(target, action, confirmActive)
    if (target === 'daemon' && action === 'install') {
      states.app = await service('app', 'start')
      await showApp()
    } else if (target === 'app' && action === 'start') await showApp()
  } catch (error) {
    await dialog.showMessageBox(window, {
      type: 'error', title: '服务操作失败', message: error.message,
      buttons: ['确定'],
    })
  } finally {
    busy = false
    await refresh()
  }
}

function serviceMenu(target, title) {
  const state = states[target].state
  return {
    label: `${title}：${label(state)}`,
    submenu: [
      ...(target === 'daemon' && states.daemon.startup_task === 'not_installed'
        ? [{ label: '设置小内核登录启动', enabled: !busy, click: () => run('daemon', 'install') }]
        : []),
      { label: `启动${title}`, enabled: !busy && state === 'stopped', click: () => run(target, 'start') },
      { label: `重启${title}`, enabled: !busy && state === 'running', click: () => run(target, 'restart') },
      { label: `停止${title}`, enabled: !busy && ['running', 'unhealthy'].includes(state), click: () => run(target, 'stop') },
      { type: 'separator' },
      { label: '查看日志', click: () => openLog(target) },
    ],
  }
}

function rebuildMenu() {
  if (!tray) return
  tray.setToolTip(`星序 · 大内核${label(states.app.state)} · 小内核${label(states.daemon.state)}`)
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: '打开星序', click: showApp },
    { label: '复制访问令牌', click: copyAccessToken },
    {
      label: '开机启动',
      type: 'checkbox',
      enabled: app.isPackaged,
      checked: app.getLoginItemSettings().openAtLogin,
      click: item => setStartupEnabled(item.checked),
    },
    { type: 'separator' },
    serviceMenu('app', '大内核'),
    serviceMenu('daemon', '小内核'),
    { type: 'separator' },
    { label: '退出星序', enabled: !busy, click: quit },
  ]))
}

function startupSettings() {
  return { available: app.isPackaged, enabled: app.getLoginItemSettings().openAtLogin }
}

function setStartupEnabled(enabled) {
  if (!app.isPackaged) throw new Error('开机启动仅支持已安装的桌面客户端')
  app.setLoginItemSettings({ openAtLogin: enabled, path: process.execPath })
  rebuildMenu()
  return startupSettings()
}

function copyAccessToken() {
  execFile(python, [tokenScript], { cwd: root, windowsHide: true, timeout: 5000 }, async (error, stdout) => {
    const token = stdout.trim()
    if (error || !token) {
      await dialog.showMessageBox(window, { type: 'error', message: '读取访问令牌失败', buttons: ['确定'] })
      return
    }
    clipboard.writeText(token)
    await dialog.showMessageBox(window, { message: '访问令牌已复制', buttons: ['确定'] })
  })
}

async function openLog(target) {
  const file = path.join(root, '.runtime', target === 'app' ? 'production.log' : 'session-daemon.log')
  if (existsSync(file)) await shell.openPath(file)
  else await dialog.showMessageBox(window, { message: '日志文件尚未生成', buttons: ['确定'] })
}

async function showApp() {
  window.show()
  if (states.app.state === 'running') {
    try {
      await window.loadURL(appUrl)
      return
    } catch {}
  }
  await window.loadFile(statusFile)
}

async function quit() {
  quitting = true
  closePreview()
  busy = true
  rebuildMenu()
  try {
    await service('app', 'stop')
    app.quit()
  } catch (error) {
    quitting = false
    busy = false
    await dialog.showMessageBox(window, {
      type: 'error', title: '无法退出星序', message: error.message,
      detail: '大内核仍在运行，请重试。', buttons: ['确定'],
    })
    rebuildMenu()
  }
}

function trustedStatusPage(event) {
  return event.senderFrame.url === statusUrl
}

ipcMain.handle('services:status', async event => {
  if (!trustedStatusPage(event)) throw new Error('不允许从当前页面管理服务')
  await refresh()
  return states
})
ipcMain.handle('services:run', async (event, target, action) => {
  if (!trustedStatusPage(event)
    || !['app', 'daemon'].includes(target)
    || !['start', 'stop', 'restart', 'install'].includes(action)
    || (action === 'install' && target !== 'daemon')) {
    throw new Error('无效的服务操作')
  }
  await run(target, action)
  return states
})
ipcMain.handle('startup:get', () => startupSettings())
ipcMain.handle('startup:set', (_event, enabled) => {
  if (typeof enabled !== 'boolean') throw new Error('无效的开机启动设置')
  return setStartupEnabled(enabled)
})
ipcMain.handle('preview:open', (_event, payload) => openPreview(payload))
ipcMain.handle('preview:resolve', (_event, url) => fetchPreviewImage(url))
ipcMain.on('preview:close', () => closePreview())
ipcMain.handle('preview:copy', (_event, dataUrl) => {
  if (!dataUrl) return false
  try {
    const img = nativeImage.createFromDataURL(dataUrl)
    clipboard.writeImage(img)
    return true
  } catch {
    return false
  }
})
ipcMain.handle('clipboard:set-files', async (_event, paths) => {
  if (!Array.isArray(paths) || paths.length === 0) return false
  const validPaths = paths.filter(p => typeof p === 'string' && p.trim())
  if (validPaths.length === 0) return false
  const psScript = `
$paths = $env:ASTRORDER_CLIP_FILES -split ";" | Where-Object { $_.Trim() -ne "" }
Add-Type -AssemblyName System.Windows.Forms
$strCol = New-Object System.Collections.Specialized.StringCollection
foreach ($p in $paths) { $strCol.Add($p.Trim()) }
if ($strCol.Count -gt 0) {
    $data = New-Object System.Windows.Forms.DataObject
    $data.SetFileDropList($strCol)
    [System.Windows.Forms.Clipboard]::SetDataObject($data, $true)
    Write-Output "OK"
}
`
  return new Promise((resolve) => {
    execFile('powershell', ['-NoProfile', '-STA', '-Command', psScript], {
      env: { ...process.env, ASTRORDER_CLIP_FILES: validPaths.join(';') },
      windowsHide: true,
      timeout: 5000,
    }, (err, stdout) => {
      if (err) {
        console.error('Failed to set clipboard file drop list:', err)
        resolve(false)
      } else {
        resolve(stdout.trim().includes('OK'))
      }
    })
  })
})

ipcMain.handle('preview:save', async (_event, dataUrl) => {
  if (!dataUrl || !previewWindow || previewWindow.isDestroyed()) return false
  try {
    const { filePath, canceled } = await dialog.showSaveDialog(previewWindow, {
      title: '保存图片',
      defaultPath: 'astrorder-image-' + Date.now() + '.png',
      filters: [
        { name: 'PNG 图像', extensions: ['png'] },
        { name: 'JPEG 图像', extensions: ['jpg', 'jpeg'] },
        { name: '所有文件', extensions: ['*'] },
      ],
    })
    if (canceled || !filePath) return false
    const img = nativeImage.createFromDataURL(dataUrl)
    const isJpg = filePath.toLowerCase().endsWith('.jpg') || filePath.toLowerCase().endsWith('.jpeg')
    const buf = isJpg ? img.toJPEG(90) : img.toPNG()
    writeFileSync(filePath, buf)
    return true
  } catch {
    return false
  }
})
ipcMain.handle('window:minimize', () => {
  if (window && !window.isDestroyed()) window.minimize()
})
ipcMain.handle('window:maximize', () => {
  if (!window || window.isDestroyed()) return false
  if (window.isMaximized()) {
    window.unmaximize()
    return false
  }
  window.maximize()
  return true
})
ipcMain.handle('window:close', () => {
  if (window && !window.isDestroyed()) window.close()
})
ipcMain.handle('window:isMaximized', () => {
  return window && !window.isDestroyed() ? window.isMaximized() : false
})
ipcMain.handle('notification:show', (_event, { title, body, tag }) => {
  try {
    if (window && !window.isDestroyed()) {
      window.flashFrame(true)
    }
    const publicDir = path.join(root || path.resolve(__dirname, '..'), 'frontend', 'public')
    const pngPath = path.join(publicDir, 'pwa-512.png')
    const icoPath = path.join(publicDir, 'favicon.ico')
    const brandIcon = existsSync(pngPath) ? pngPath : existsSync(icoPath) ? icoPath : undefined

    // 1. 尝试现代 Windows Toast 通知
    if (Notification.isSupported()) {
      try {
        const notif = new Notification({
          title: title || '星序',
          body: body || '',
          icon: brandIcon,
          silent: false,
        })
        notif.on('click', () => {
          if (window && !window.isDestroyed()) {
            window.flashFrame(false)
            if (window.isMinimized()) window.restore()
            window.show()
            window.focus()
          }
        })
        notif.show()
      } catch (toastErr) {
        console.error('Failed to display Windows Toast', toastErr)
      }
    }

    // 2. 开发模式或 Windows 系统备用：使用无蓝色大圆圈的极简优雅气泡通知
    if (tray && process.platform === 'win32' && (!app.isPackaged || !Notification.isSupported())) {
      try {
        tray.displayBalloon({
          iconType: 'none',
          title: title || '星序',
          content: body || '',
          noSound: false,
        })
      } catch (trayErr) {
        console.error('Failed to display tray balloon', trayErr)
      }
    }
    return true
  } catch (err) {
    console.error('Failed to display native notification', err)
    return false
  }
})

if (!app.requestSingleInstanceLock()) app.quit()
else {
  app.on('second-instance', () => showApp())
  if (process.platform === 'win32') {
  app.setAppUserModelId(app.isPackaged ? 'com.astrorder.desktop' : '星序')
}

app.whenReady().then(async () => {
    Menu.setApplicationMenu(null)
    await configureRoot()
    await session.defaultSession.clearCache()
    await session.defaultSession.clearStorageData({ storages: ['serviceworkers'] })
    const initialTheme = getStoredTheme()
    const isDark = initialTheme === 'dark'
    window = new BrowserWindow({
      backgroundColor: isDark ? '#15181e' : '#f9fafc',
      width: 1440, height: 900, minWidth: 960, minHeight: 640,
      icon: path.join(root, 'frontend', 'public', 'favicon.ico'),
      webPreferences: {
        preload: path.join(__dirname, 'preload.js'),
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
      },
    })
    void window.loadFile(splashFile)
    if (initialTheme) {
      window.webContents.once('did-finish-load', () => {
        void window.webContents.executeJavaScript(`document.documentElement.setAttribute('data-theme', '${initialTheme}')`)
      })
    }
    window.on('close', event => {
      if (!quitting) {
        event.preventDefault()
        window.hide()
      }
    })
    window.on('hide', () => closePreview())
    window.webContents.on('before-input-event', (event, input) => {
      if (input.type !== 'keyDown') return
      const isCtrlOrCmd = process.platform === 'darwin' ? input.meta : input.control
      if ((isCtrlOrCmd && input.key.toLowerCase() === 'r') || input.key === 'F5') {
        event.preventDefault()
        window.webContents.reloadIgnoringCache()
      } else if ((isCtrlOrCmd && input.shift && input.key.toLowerCase() === 'i') || input.key === 'F12') {
        event.preventDefault()
        window.webContents.toggleDevTools()
      }
    })
    window.webContents.setWindowOpenHandler(({ url }) => {
      if (url.startsWith('http://') || url.startsWith('https://')) shell.openExternal(url)
      return { action: 'deny' }
    })
    window.webContents.on('will-navigate', (event, url) => {
      if (url !== appUrl && !url.startsWith(`${appUrl}/`) && url !== statusUrl && url !== splashUrl) event.preventDefault()
    })
    tray = new Tray(path.join(root, 'frontend', 'public', 'favicon.ico'))
    tray.on('double-click', showApp)
    tray.on('balloon-click', showApp)
    rebuildMenu()
    await refresh()
    if (states.daemon.startup_task !== 'not_installed') {
      try {
        states.daemon = await service('daemon', 'start')
        states.app = await service('app', 'start')
      } catch (error) {
        await dialog.showMessageBox(window, {
          type: 'error', title: '星序启动失败', message: error.message,
          detail: '可在托盘菜单中查看状态或日志。', buttons: ['确定'],
        })
      }
    }
    await refresh()
    await showApp()
    setInterval(refresh, 5000).unref()
  }).catch(error => {
    dialog.showErrorBox('星序启动失败', error.message)
    app.exit(1)
  })
}

app.on('window-all-closed', event => event.preventDefault())
