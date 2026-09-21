const { contextBridge, ipcRenderer, webUtils } = require('electron')

contextBridge.exposeInMainWorld('astrorderDesktop', {
  status: () => ipcRenderer.invoke('services:status'),
  run: (target, action) => ipcRenderer.invoke('services:run', target, action),
  openPreview: (payload) => ipcRenderer.invoke('preview:open', payload),
  minimize: () => ipcRenderer.invoke('window:minimize'),
  maximize: () => ipcRenderer.invoke('window:maximize'),
  close: () => ipcRenderer.invoke('window:close'),
  isMaximized: () => ipcRenderer.invoke('window:isMaximized'),
  notify: (payload) => ipcRenderer.invoke('notification:show', payload),
  setClipboardFiles: (paths) => ipcRenderer.invoke('clipboard:set-files', paths),
  getPathForFile: (file) => webUtils.getPathForFile(file),
})

window.addEventListener('DOMContentLoaded', () => {
  document.documentElement.classList.add('is-desktop-app')
})
