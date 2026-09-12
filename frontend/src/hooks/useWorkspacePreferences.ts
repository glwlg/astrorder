import { useCallback, useEffect, useRef, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import { loadPinnedProjects, loadProjectAppearance, type ProjectAppearanceMap } from '../components/projectAppearance'
import { readProjectOrder } from '../components/projectOrder'

export type WorkspacePreferences = {
  appearance: ProjectAppearanceMap
  session_pins: Record<string, boolean>
  pinned_projects: string[]
  project_order: string[]
}
export type PreferencePatch = Partial<Omit<WorkspacePreferences, 'appearance'>> & {
  appearance?: Record<string, ProjectAppearanceMap[string] | null>
}

function legacyPreferences(): WorkspacePreferences {
  let pins: Record<string, boolean> = {}
  try { pins = JSON.parse(localStorage.getItem('astrorder_pinned_sessions') || '{}') } catch {}
  return { appearance: loadProjectAppearance(), session_pins: pins, pinned_projects: loadPinnedProjects(), project_order: readProjectOrder() }
}

export function useWorkspacePreferences() {
  const [preferences, setPreferences] = useState(legacyPreferences)
  const current = useRef(preferences)
  const queue = useRef(Promise.resolve())
  const mounted = useRef(false)
  const ready = useRef(false)
  const report = (error: unknown) => { notifications.show({ id: 'workspace-preferences-error', color: 'red', message: `偏好设置同步失败：${error instanceof Error ? error.message : '请重试'}` }) }
  const accept = useCallback((next: WorkspacePreferences) => {
    current.current = next
    if (mounted.current) setPreferences(next)
  }, [])
  useEffect(() => {
    mounted.current = true
    const refresh = () => {
      queue.current = queue.current.then(async () => {
        if (!mounted.current) return
        if (!ready.current) {
          const legacy = legacyPreferences()
          const hasLegacy = Object.values(legacy).some(value => Object.keys(value).length > 0)
          const result = hasLegacy ? await api.importPreferences(legacy) : await api.getPreferences()
          accept(result)
          ready.current = true
        } else {
          accept(await api.getPreferences())
        }
      }).catch(report)
    }
    refresh()
    const timer = window.setInterval(refresh, 10000)
    window.addEventListener('focus', refresh)
    return () => { mounted.current = false; window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [accept])
  const updatePreferences = useCallback((patch: PreferencePatch | ((value: WorkspacePreferences) => PreferencePatch)) => {
    queue.current = queue.current.then(async () => {
      if (!ready.current) throw new Error('尚未读取服务端设置，请稍后重试')
      accept(await api.updatePreferences(typeof patch === 'function' ? patch(current.current) : patch))
    }).catch(report)
    return queue.current
  }, [accept])
  const removeProjectPreferences = (key: string) => updatePreferences(value => ({
    appearance: { [key]: null },
    pinned_projects: value.pinned_projects.filter(item => item !== key),
    project_order: value.project_order.filter(item => item !== key),
  }))
  return { preferences, updatePreferences, removeProjectPreferences }
}
