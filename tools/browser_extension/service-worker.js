const PREFIX = 'astrorder:'
const owners = new Map()
let queue = Promise.resolve()

function decodeMarker(marker) {
  if (typeof marker !== 'string' || !marker.startsWith(PREFIX)) return null
  try {
    const encoded = marker.slice(PREFIX.length).replace(/-/g, '+').replace(/_/g, '/')
    const key = new TextDecoder().decode(Uint8Array.from(atob(encoded), char => char.charCodeAt(0)))
    return key.includes('::') ? key : null
  } catch { return null }
}
async function savedOwners() {
  const saved = await chrome.storage.session.get('owners')
  for (const [id, key] of Object.entries(saved.owners || {})) if (!owners.has(Number(id))) owners.set(Number(id), key)
}
async function saveOwners() {
  await chrome.storage.session.set({ owners: Object.fromEntries(owners) })
}
async function bindTab(tabId, marker) {
  await savedOwners()
  let key = decodeMarker(marker)
  if (!key) {
    try {
      const [{ result }] = await chrome.scripting.executeScript({ target: { tabId }, func: () => window.name })
      key = decodeMarker(result)
    } catch { /* Restricted pages retain their extension-owned binding. */ }
  }
  key ||= owners.get(tabId)
  if (!key) return
  const tab = await chrome.tabs.get(tabId)
  owners.set(tabId, key)
  await saveOwners()
  const peers = await chrome.tabs.query({ windowId: tab.windowId })
  let groupId
  for (const peer of peers) {
    if (peer.id !== tabId && owners.get(peer.id) === key && peer.groupId !== chrome.tabGroups.TAB_GROUP_ID_NONE) {
      groupId = peer.groupId
      break
    }
  }
  if (groupId === undefined && tab.groupId !== chrome.tabGroups.TAB_GROUP_ID_NONE) {
    const conflicting = peers.some(peer => peer.groupId === tab.groupId && owners.get(peer.id) && owners.get(peer.id) !== key)
    if (!conflicting) groupId = tab.groupId
  }
  groupId = await chrome.tabs.group({ ...(groupId === undefined ? {} : { groupId }), tabIds: [tabId] })
  await chrome.tabGroups.update(groupId, { title: `星序 · ${key.split('::').at(-1).slice(0, 8)}`, color: 'blue', collapsed: false })
}
function schedule(tabId, marker) {
  const task = queue.catch(() => {}).then(() => bindTab(tabId, marker))
  queue = task
  return task.catch(error => console.warn('Astrorder tab binding failed:', error.message))
}
chrome.tabs.onUpdated.addListener((tabId, change) => {
  if (change.status === 'complete') return schedule(tabId)
})
chrome.tabs.onCreated.addListener(tab => {
  if (!tab.openerTabId) return
  return savedOwners().then(async () => {
    const key = owners.get(tab.openerTabId)
    if (key) { owners.set(tab.id, key); await saveOwners(); return schedule(tab.id) }
  }).catch(error => console.warn('Astrorder popup binding failed:', error.message))
})
chrome.tabs.onRemoved.addListener(tabId => { owners.delete(tabId); void saveOwners() })
chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (message?.type !== 'astrorder.bind' || !sender.tab || sender.frameId !== 0) return
  schedule(sender.tab.id, message.marker).then(() => respond({ ok: true }))
  return true
})
async function restore() {
  await savedOwners()
  for (const tab of await chrome.tabs.query({})) await schedule(tab.id)
}
chrome.runtime.onStartup.addListener(restore)
chrome.runtime.onInstalled.addListener(restore)
void restore().catch(error => console.warn('Astrorder session restore failed:', error.message))
