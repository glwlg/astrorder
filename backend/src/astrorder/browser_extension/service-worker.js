const PREFIX = 'astrorder:'

async function groupAstrorderTab(tabId) {
  try {
    const [{ result: marker }] = await chrome.scripting.executeScript({
      target: { tabId },
      func: () => window.name,
    })
    if (typeof marker !== 'string' || !marker.startsWith(PREFIX)) return
    const encoded = marker.slice(PREFIX.length)
    const key = decodeURIComponent(escape(atob(encoded.replace(/-/g, '+').replace(/_/g, '/'))))
    const label = `星序 · ${key.split('::').at(-1).slice(0, 8)}`
    const tab = await chrome.tabs.get(tabId)
    const groupId = tab.groupId === chrome.tabGroups.TAB_GROUP_ID_NONE
      ? await chrome.tabs.group({ tabIds: [tabId] })
      : tab.groupId
    await chrome.tabGroups.update(groupId, { title: label, color: 'blue', collapsed: false })
  } catch (_) {}
}

chrome.tabs.onUpdated.addListener((tabId, change) => {
  if (change.status === 'complete') groupAstrorderTab(tabId)
})

chrome.tabs.onCreated.addListener(tab => {
  if (tab.openerTabId) {
    chrome.tabs.get(tab.openerTabId).then(opener => {
      if (opener.groupId !== chrome.tabGroups.TAB_GROUP_ID_NONE) {
        chrome.tabs.group({ groupId: opener.groupId, tabIds: [tab.id] })
      }
    }).catch(() => {})
  }
})
