const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('astrorderPreview', {
  onData: (cb) => {
    ipcRenderer.on('preview:data', (_event, payload) => cb(payload))
  },
  resolve: (url) => ipcRenderer.invoke('preview:resolve', url),
  close: () => ipcRenderer.send('preview:close'),
  copy: (dataUrl) => ipcRenderer.invoke('preview:copy', dataUrl),
  save: (dataUrl) => ipcRenderer.invoke('preview:save', dataUrl),
})
