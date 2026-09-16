const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('astrorderDesktop', {
  status: () => ipcRenderer.invoke('services:status'),
  run: (target, action) => ipcRenderer.invoke('services:run', target, action),
  setTheme: (theme) => ipcRenderer.invoke('window:set-theme', theme),
  setOverlay: (options) => ipcRenderer.invoke('window:set-overlay', options),
  openPreview: (payload) => ipcRenderer.invoke('preview:open', payload),
  minimize: () => ipcRenderer.invoke('window:minimize'),
  maximize: () => ipcRenderer.invoke('window:maximize'),
  close: () => ipcRenderer.invoke('window:close'),
  isMaximized: () => ipcRenderer.invoke('window:isMaximized'),
})

window.addEventListener('DOMContentLoaded', () => {
  document.documentElement.classList.add('is-desktop-app')
})
