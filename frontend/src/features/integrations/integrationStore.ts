import { create } from 'zustand'
import { api, type IntegrationItem, type IntegrationPayload } from '../../api/client'

export interface IntegrationsState {
  items: IntegrationItem[]
  hydrated: boolean
  loading: boolean
  hydrate: (force?: boolean) => Promise<void>
  create: (payload: IntegrationPayload) => Promise<IntegrationItem>
  update: (id: string, payload: Partial<IntegrationPayload>) => Promise<IntegrationItem>
  remove: (id: string) => Promise<void>
  reorder: (ids: string[]) => Promise<void>
  reset: () => void
}

let hydratePromise: Promise<void> | null = null

export const useIntegrationsStore = create<IntegrationsState>((set, get) => ({
  items: [],
  hydrated: false,
  loading: false,

  hydrate: async (force = false) => {
    if (!force && get().hydrated) return
    if (hydratePromise) return hydratePromise
    set({ loading: true })
    hydratePromise = (async () => {
      try {
        const res = await api.listIntegrations()
        set({ items: Array.isArray(res?.items) ? res.items : [], hydrated: true, loading: false })
      } catch {
        set({ loading: false })
      } finally {
        hydratePromise = null
      }
    })()
    return hydratePromise
  },

  create: async (payload) => {
    const created = await api.createIntegration(payload)
    set((state) => ({
      items: [...state.items.filter((item) => item.id !== created.id), created].sort((a, b) => a.order - b.order),
      hydrated: true,
    }))
    return created
  },

  update: async (id, payload) => {
    const updated = await api.updateIntegration(id, payload)
    set((state) => ({
      items: state.items
        .map((item) => (item.id === id ? updated : item))
        .sort((a, b) => a.order - b.order),
    }))
    return updated
  },

  remove: async (id) => {
    await api.deleteIntegration(id)
    set((state) => ({
      items: state.items.filter((item) => item.id !== id).map((item, index) => ({ ...item, order: index })),
    }))
  },

  reorder: async (ids) => {
    const prev = get().items
    const byId = new Map(prev.map((item) => [item.id, item]))
    const optimistic = ids
      .map((id) => byId.get(id))
      .filter((item): item is IntegrationItem => Boolean(item))
      .map((item, index) => ({ ...item, order: index }))
    const seen = new Set(optimistic.map((item) => item.id))
    for (const item of prev) {
      if (!seen.has(item.id)) optimistic.push({ ...item, order: optimistic.length })
    }
    set({ items: optimistic })
    try {
      const res = await api.reorderIntegrations(ids)
      set({ items: Array.isArray(res?.items) ? res.items : optimistic })
    } catch (error) {
      set({ items: prev })
      throw error
    }
  },

  reset: () => {
    hydratePromise = null
    set({ items: [], hydrated: false, loading: false })
  },
}))
