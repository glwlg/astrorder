// Runs in the extension isolated world. Read the shared window.name rather
// than trusting arbitrary message payloads from application pages.
let lastMarker = ''
function syncBinding() {
  const marker = window.name
  if (!marker.startsWith('astrorder:') || marker === lastMarker) return
  lastMarker = marker
  chrome.runtime.sendMessage({ type: 'astrorder.bind', marker }).catch(() => { lastMarker = '' })
}
window.addEventListener('astrorder:session-bound', syncBinding)
window.addEventListener('pageshow', syncBinding)
setInterval(syncBinding, 1000)
syncBinding()
