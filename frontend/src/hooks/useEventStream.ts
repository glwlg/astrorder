import { useEffect } from 'react'
import { notifications as mantineNotifications } from '@mantine/notifications'
import type { QueryClient } from '@tanstack/react-query'
import { connectEventStream, type EventStreamStatus } from '../api/eventStream'
import { deliverBrowserNotification, notificationForEvent } from '../domain/notifications'
import { useAstrorderStore } from '../state/store'
import { api } from '../api/client'
import { nativeActivityEvent } from './useSessionOrder'

function storeStatus(status: EventStreamStatus) {
  const connection = status === 'connected' ? 'connected' : status === 'error' ? 'error' : status
  useAstrorderStore.getState().setConnection(connection)
}

export function useEventStream(authenticated: boolean, queryClient: QueryClient): void {
  useEffect(() => {
    if (!authenticated) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const result = await api.getPresence()
        if (!stopped) window.dispatchEvent(new CustomEvent(nativeActivityEvent, { detail: result }))
      } catch { /* A temporarily unavailable source must not discard its last activity. */ }
      if (!stopped) timer = setTimeout(() => void poll(), 5000)
    }
    timer = setTimeout(() => void poll(), 2000)
    return () => { stopped = true; clearTimeout(timer) }
  }, [authenticated])
  useEffect(() => {
    if (!authenticated) {
      useAstrorderStore.getState().setConnection('disconnected')
      return undefined
    }

    const stop = connectEventStream({
      after: useAstrorderStore.getState().cursor,
      onStatus: storeStatus,
      onEvent: (event) => {
        const store = useAstrorderStore.getState()
        const notification = event.cursor > store.cursor ? notificationForEvent(event) : null
        if (notification && store.addNotification(notification)) {
          mantineNotifications.show({
            id: notification.key,
            title: notification.title,
            message: notification.message,
            color: notification.kind === 'task_failed' ? 'red' : notification.kind === 'approval_pending' ? 'yellow' : 'teal',
            autoClose: 8000,
          })
          deliverBrowserNotification(notification)
        }
        store.applyEvent(event)
        if (
          event.type === 'command.upsert'
          && ['completed', 'failed', 'cancelled'].includes(String(event.data.state))
        ) {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', event.agent_id, event.session_id] })
        }
        if (event.type === 'native.observation') {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'observations', event.agent_id] })
          if (event.data.event === 'UserPromptSubmit' && typeof event.data.observed_at === 'number') {
            window.dispatchEvent(new CustomEvent(nativeActivityEvent, { detail: [{ agent_id: event.agent_id, id: event.session_id, last_user_at: new Date(event.data.observed_at * 1000).toISOString() }] }))
          }
          if (event.data.event === 'Stop') void queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', event.agent_id, event.session_id] })
        }
        if (event.type === 'sidecar.plugin.control' && event.data) {
          const sidecar = useSidecarStore.getState()
          const payload = event.data as Record<string, unknown>
          const action = payload.action
          if (action === 'open') {
            const pid = String(payload.plugin_id || '')
            const sid = String(payload.session_id || 'current')
            const aid = String(payload.agent_id || '')
            const titleStr = typeof payload.title === 'string' ? payload.title : undefined
            const pathStr = typeof payload.path === 'string' ? payload.path : undefined
            const urlStr = typeof payload.url === 'string' ? payload.url : undefined
            if (pid === 'terminal') {
              sidecar.openTerminal(sid, aid, titleStr)
            } else if (pid === 'browser') {
              sidecar.openBrowser(sid, aid, urlStr)
            } else if (pid === 'gitdiff') {
              sidecar.openGitDiff(sid, aid, pathStr)
            } else if (pid === 'filetree') {
              sidecar.openFileTree(sid, aid, pathStr)
            } else if (pid === 'sidechat') {
              sidecar.openSideChat(sid, aid, titleStr)
            } else if (pid === 'agentgraph') {
              sidecar.openAgentGraph(sid, aid, titleStr)
            } else {
              // Artifact viewers like drawio, mermaid, excalidraw, diff, three, html, monaco
              const filePath = pathStr || titleStr || pid
              const name = filePath.split(/[\\/]/).pop() || filePath
              const artifact: ArtifactRef = {
                id: `artifact:${pid}:${sid}:${filePath}`,
                name: titleStr || name,
                kind: 'workspace_file',
                path: filePath,
                mediaType: 'text/plain',
                readUrl: urlStr || '',
                writable: true,
                sessionId: sid,
                agentId: aid,
              }
              sidecar.openArtifact(artifact, `${pid}-viewer`)
            }
          } else if (action === 'close') {
            if (payload.collapse) {
              sidecar.setIsOpen(false)
            } else if (payload.tab_id) {
              sidecar.closeTab(String(payload.tab_id))
            }
          }
        }
        if (useAstrorderStore.getState().resyncRequired) {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages'] })
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands'] })
        }
      },
    })
    return stop
  }, [authenticated, queryClient])
}
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import type { ArtifactRef } from '../domain/artifact'
