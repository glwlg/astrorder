import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import type { ArtifactRef } from '../../domain/artifact'

export type SidecarTabType = 'details' | 'artifact'

export interface SidecarTab {
  id: string
  type: SidecarTabType
  title: string
  artifact?: ArtifactRef
  viewerId?: string
  closable: boolean
}

export interface SessionSidecarMemory {
  tabs: SidecarTab[]
  activeTabId: string
  isOpen: boolean
}

export interface SidecarState {
  isOpen: boolean
  activeTabId: string
  tabs: SidecarTab[]
  dirtyTabs: Record<string, boolean>
  sidecarWidth: number
  activeSessionKey: string | null
  sessionMemories: Record<string, SessionSidecarMemory>
  mountedTabs: Record<string, SidecarTab[]>
  tabCloseHandlers: Record<string, () => void>
  openArtifact: (artifact: ArtifactRef, viewerId: string) => void
  openDetails: () => void
  closeTab: (tabId: string) => void
  setActiveTabId: (tabId: string) => void
  setIsOpen: (open: boolean) => void
  setTabDirty: (tabId: string, dirty: boolean) => void
  setSidecarWidth: (width: number) => void
  openTerminal: (sessionId: string, agentId: string, workspaceName?: string, connectionId?: string) => void
  openFileTree: (sessionId: string, agentId: string, workspacePath?: string, workspaceName?: string, connectionId?: string) => void
  openSideChat: (sessionId: string, agentId: string, workspaceName?: string, connectionId?: string) => void
  openAgentGraph: (sessionId: string, agentId: string, workspaceName?: string, connectionId?: string) => void
  openGitDiff: (sessionId: string, agentId: string, workspacePath?: string, connectionId?: string) => void
  openBrowser: (sessionId: string, agentId: string, initialUrl?: string, connectionId?: string) => void
  switchSession: (sessionKey: string) => void
  resetSessionSidecar: () => void
  registerTabCloseHandler: (agentId: string, tabId: string, handler: () => void) => () => void
}

function tabLifecycleKey(agentId: string, tabId: string) {
  return JSON.stringify([agentId, tabId])
}

const unavailableStorage = {
  getItem: (_key: string) => null,
  setItem: (_key: string, _value: string) => undefined,
  removeItem: (_key: string) => undefined,
}

function sidecarStorage() {
  try {
    const storage = globalThis.localStorage
    if (
      typeof storage?.getItem === 'function'
      && typeof storage?.setItem === 'function'
      && typeof storage?.removeItem === 'function'
    ) return storage
  } catch {
    // Persistent storage can be unavailable in privacy-restricted contexts.
  }
  return unavailableStorage
}

export const useSidecarStore = create<SidecarState>()(persist((set, get) => ({
  isOpen: false,
  activeTabId: '',
  tabs: [],
  dirtyTabs: {},
  sidecarWidth: 50, // 默认 50% 宽度（对半平分）
  activeSessionKey: null,
  sessionMemories: {},
  mountedTabs: {},
  tabCloseHandlers: {},

  switchSession: (sessionKey: string) => {
    const { activeSessionKey, tabs, activeTabId, isOpen, sessionMemories, mountedTabs } = get()
    if (activeSessionKey === sessionKey) return

    // 1. 保存前一个会话的侧边栏状态（标签页、活动Tab、展开状态）
    const nextMemories = { ...sessionMemories }
    const nextMountedTabs = { ...mountedTabs }
    if (activeSessionKey) {
      nextMemories[activeSessionKey] = {
        tabs,
        activeTabId,
        isOpen,
      }
      nextMountedTabs[activeSessionKey] = tabs
    }

    // 2. 恢复目标会话的记忆状态，若无记忆则默认 tabs 为空、activeTabId 为空（展示 Codex 菜单）
    const targetMemory = nextMemories[sessionKey]
    if (targetMemory) {
      set({
        activeSessionKey: sessionKey,
        sessionMemories: nextMemories,
        mountedTabs: { ...nextMountedTabs, [sessionKey]: targetMemory.tabs || [] },
        tabs: targetMemory.tabs || [],
        activeTabId: targetMemory.activeTabId || '',
        isOpen: targetMemory.isOpen,
      })
    } else {
      set({
        activeSessionKey: sessionKey,
        sessionMemories: nextMemories,
        mountedTabs: { ...nextMountedTabs, [sessionKey]: [] },
        tabs: [],
        activeTabId: '',
        isOpen: false,
      })
    }
  },

  resetSessionSidecar: () => {
    set({
      activeTabId: '',
      tabs: [],
      dirtyTabs: {},
    })
  },

  openFileTree: (sessionId: string, agentId: string, workspacePath?: string, workspaceName?: string, connectionId?: string) => {
    const tabId = `filetree:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const fileTreeArtifact: ArtifactRef = {
        id: tabId,
        name: workspaceName ? `文件 (${workspaceName})` : '工作区文件',
        kind: 'workspace_file',
        mediaType: 'application/x-directory',
        readUrl: '',
        writable: false,
        path: workspacePath || '.',
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: workspaceName ? `文件 (${workspaceName})` : '工作区文件',
        artifact: fileTreeArtifact,
        viewerId: 'filetree-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openAgentGraph: (sessionId: string, agentId: string, _workspaceName?: string, connectionId?: string) => {
    const tabId = `agentgraph:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const graphArtifact: ArtifactRef = {
        id: tabId,
        name: '决策状态机',
        kind: 'workspace_file',
        mediaType: 'application/x-agent-graph',
        readUrl: '',
        writable: false,
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: '决策状态机',
        artifact: graphArtifact,
        viewerId: 'agent-graph-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openGitDiff: (sessionId: string, agentId: string, workspacePath?: string, connectionId?: string) => {
    const tabId = `gitdifftree:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const gitArtifact: ArtifactRef = {
        id: tabId,
        name: '代码变更',
        kind: 'workspace_file',
        mediaType: 'application/x-git-diff-tree',
        readUrl: '',
        writable: false,
        path: workspacePath,
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: '代码变更',
        artifact: gitArtifact,
        viewerId: 'git-diff-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openTerminal: (sessionId: string, agentId: string, workspaceName?: string, connectionId?: string) => {
    const tabId = `terminal:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const terminalArtifact: ArtifactRef = {
        id: tabId,
        name: workspaceName ? `终端 (${workspaceName})` : '终端',
        kind: 'workspace_file',
        mediaType: 'application/x-terminal',
        readUrl: '',
        writable: false,
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: workspaceName ? `终端 (${workspaceName})` : '终端',
        artifact: terminalArtifact,
        viewerId: 'xterm-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openSideChat: (sessionId: string, agentId: string, _workspaceName?: string, connectionId?: string) => {
    const tabId = `sidechat:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const chatArtifact: ArtifactRef = {
        id: tabId,
        name: '侧边聊天',
        kind: 'workspace_file',
        mediaType: 'application/x-astrorder-side-chat',
        readUrl: '',
        writable: false,
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: '侧边聊天',
        artifact: chatArtifact,
        viewerId: 'sidecar-chat-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openBrowser: (sessionId: string, agentId: string, initialUrl?: string, connectionId?: string) => {
    const tabId = `browser:${sessionId}`
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === tabId)
    const url = initialUrl || 'https://www.bing.com'
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: tabId,
      })
    } else {
      const browserArtifact: ArtifactRef = {
        id: tabId,
        name: '浏览器',
        kind: 'remote_url',
        mediaType: 'text/html',
        readUrl: url,
        writable: false,
        sessionId,
        agentId,
        connectionId,
      }
      const newTab: SidecarTab = {
        id: tabId,
        type: 'artifact',
        title: '浏览器',
        artifact: browserArtifact,
        viewerId: 'html-preview-viewer',
        closable: true,
      }
      set({
        isOpen: true,
        activeTabId: tabId,
        tabs: [...tabs, newTab],
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openArtifact: (artifact: ArtifactRef, viewerId: string) => {
    const { tabs, activeSessionKey, sessionMemories } = get()
    const existingIndex = tabs.findIndex((t) => t.id === artifact.id)
    if (existingIndex >= 0) {
      set({
        isOpen: true,
        activeTabId: artifact.id,
      })
    } else {
      const newTab: SidecarTab = {
        id: artifact.id,
        type: 'artifact',
        title: artifact.name,
        artifact,
        viewerId,
        closable: true,
      }
      const nextTabs = [...tabs, newTab]
      set({
        isOpen: true,
        activeTabId: artifact.id,
        tabs: nextTabs,
      })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  openDetails: () => {
    const { tabs, activeSessionKey, sessionMemories } = get()
    const detailsTab = tabs.find((t) => t.id === 'details')
    if (!detailsTab) {
      set({
        isOpen: true,
        activeTabId: 'details',
        tabs: [
          ...tabs,
          {
            id: 'details',
            type: 'details',
            title: '会话详情',
            closable: true,
          },
        ],
      })
    } else {
      set({ isOpen: true, activeTabId: 'details' })
    }
    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: true },
        },
      })
    }
  },

  closeTab: (tabId: string) => {
    const {
      tabs,
      activeTabId,
      dirtyTabs,
      activeSessionKey,
      sessionMemories,
      mountedTabs,
      tabCloseHandlers,
    } = get()
    const closingTab = tabs.find((tab) => tab.id === tabId)
    const lifecycleKey = closingTab?.artifact?.agentId
      ? tabLifecycleKey(closingTab.artifact.agentId, tabId)
      : null
    if (lifecycleKey) {
      try {
        tabCloseHandlers[lifecycleKey]?.()
      } catch {
        // A plugin cleanup failure must not trap an otherwise closable tab.
      }
    }
    const nextTabs = tabs.filter((t) => t.id !== tabId)
    const nextDirty = { ...dirtyTabs }
    const nextCloseHandlers = { ...tabCloseHandlers }
    delete nextDirty[tabId]
    if (lifecycleKey) delete nextCloseHandlers[lifecycleKey]

    let nextActiveId = activeTabId
    if (activeTabId === tabId) {
      const closedIndex = tabs.findIndex((t) => t.id === tabId)
      const fallbackTab = tabs[closedIndex - 1] || tabs[closedIndex + 1]
      nextActiveId = fallbackTab ? fallbackTab.id : ''
    }

    set({
      tabs: nextTabs,
      activeTabId: nextActiveId,
      dirtyTabs: nextDirty,
      mountedTabs: activeSessionKey
        ? { ...mountedTabs, [activeSessionKey]: nextTabs }
        : mountedTabs,
      tabCloseHandlers: nextCloseHandlers,
    })

    if (activeSessionKey) {
      const curr = get()
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs: curr.tabs, activeTabId: curr.activeTabId, isOpen: curr.isOpen },
        },
      })
    }
  },

  setActiveTabId: (tabId: string) => {
    const { activeSessionKey, sessionMemories, tabs, isOpen } = get()
    set({ activeTabId: tabId })
    if (activeSessionKey) {
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs, activeTabId: tabId, isOpen },
        },
      })
    }
  },

  setIsOpen: (open: boolean) => {
    const { activeSessionKey, sessionMemories, tabs, activeTabId } = get()
    set({ isOpen: open })
    if (activeSessionKey) {
      set({
        sessionMemories: {
          ...sessionMemories,
          [activeSessionKey]: { tabs, activeTabId, isOpen: open },
        },
      })
    }
  },

  setTabDirty: (tabId: string, dirty: boolean) => {
    set((state) => ({
      dirtyTabs: {
        ...state.dirtyTabs,
        [tabId]: dirty,
      },
    }))
  },

  registerTabCloseHandler: (agentId: string, tabId: string, handler: () => void) => {
    const key = tabLifecycleKey(agentId, tabId)
    set((state) => ({ tabCloseHandlers: { ...state.tabCloseHandlers, [key]: handler } }))
    return () => {
      const current = get().tabCloseHandlers
      if (current[key] !== handler) return
      const next = { ...current }
      delete next[key]
      set({ tabCloseHandlers: next })
    }
  },

  setSidecarWidth: (width: number) => {
    const clamped = Math.max(20, Math.min(80, width))
    if (get().sidecarWidth === clamped) return
    set({ sidecarWidth: clamped })
  },
}), {
  name: 'astrorder:sidecar:v1',
  storage: createJSONStorage(sidecarStorage),
  partialize: (state) => ({
    sessionMemories: state.sessionMemories,
    sidecarWidth: state.sidecarWidth,
  }),
}))
