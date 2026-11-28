import type { Disposable } from './types'

export interface HostStreamEvent {
  type: string
  cursor?: number
  agent_id?: string
  session_id?: string
  data?: Record<string, unknown>
  timestamp?: string
  [key: string]: unknown
}

export interface PluginEventMeta {
  fromPluginId: string
  topic: string
  timestamp: number
}

export type HostEventHandler = (event: HostStreamEvent) => void
export type PluginEventHandler<T = unknown> = (payload: T, meta: PluginEventMeta) => void

interface HostEventSubscription {
  pattern: string
  handler: HostEventHandler
}

interface PluginEventSubscription {
  topic: string
  handler: PluginEventHandler<unknown>
}

interface RegisteredServiceEntry {
  serviceId: string
  pluginId: string
  impl: unknown
}

function matchesEventPattern(pattern: string, eventType: string): boolean {
  const p = pattern.trim()
  if (!p || p === '*') return true
  if (p.endsWith('.*')) {
    const prefix = p.slice(0, -1)
    return eventType.startsWith(prefix)
  }
  if (p.endsWith('*')) {
    const prefix = p.slice(0, -1)
    return eventType.startsWith(prefix)
  }
  return p === eventType
}

class PluginEventBus {
  private hostSubscribers = new Set<HostEventSubscription>()
  private pluginSubscribers = new Set<PluginEventSubscription>()
  private services = new Map<string, RegisteredServiceEntry>()
  private serviceListeners = new Set<() => void>()
  private serviceVersion = 0

  /**
   * Dispatches a host WebSocket/domain event to subscribed plugins.
   * Each subscriber is isolated in try/catch so faulty plugin code never
   * disrupts the main event stream.
   */
  emitHostEvent(event: HostStreamEvent): void {
    if (!event || typeof event.type !== 'string' || this.hostSubscribers.size === 0) {
      return
    }
    for (const sub of Array.from(this.hostSubscribers)) {
      if (!matchesEventPattern(sub.pattern, event.type)) continue
      try {
        sub.handler(event)
      } catch (err) {
        console.error(`[pluginEventBus] host event listener failed (${event.type}):`, err)
      }
    }
  }

  onHostEvent(pattern: string, handler: HostEventHandler): Disposable {
    const sub: HostEventSubscription = { pattern: pattern || '*', handler }
    this.hostSubscribers.add(sub)
    return {
      dispose: () => {
        this.hostSubscribers.delete(sub)
      },
    }
  }

  onSessionEvent(sessionId: string, handler: HostEventHandler): Disposable {
    const targetId = sessionId.trim()
    return this.onHostEvent('*', (event) => {
      if (!targetId) {
        handler(event)
        return
      }
      const evSessionId =
        event.session_id ||
        (event.data && typeof event.data.session_id === 'string' ? event.data.session_id : undefined)
      if (evSessionId === targetId) {
        handler(event)
      }
    })
  }

  /**
   * Emits a cross-plugin pub/sub event. Topics support exact matching,
   * prefix wildcards (`"foo.*"`), or `"*"`.
   */
  emitPluginEvent(fromPluginId: string, topic: string, payload?: unknown): void {
    const cleanTopic = topic.trim()
    if (!cleanTopic || this.pluginSubscribers.size === 0) return
    const meta: PluginEventMeta = {
      fromPluginId: fromPluginId || 'host',
      topic: cleanTopic,
      timestamp: Date.now(),
    }
    for (const sub of Array.from(this.pluginSubscribers)) {
      if (!matchesEventPattern(sub.topic, cleanTopic)) continue
      try {
        sub.handler(payload, meta)
      } catch (err) {
        console.error(`[pluginEventBus] plugin event listener failed (${cleanTopic}):`, err)
      }
    }
  }

  onPluginEvent<T = unknown>(topic: string, handler: PluginEventHandler<T>): Disposable {
    const sub: PluginEventSubscription = {
      topic: topic.trim() || '*',
      handler: handler as PluginEventHandler<unknown>,
    }
    this.pluginSubscribers.add(sub)
    return {
      dispose: () => {
        this.pluginSubscribers.delete(sub)
      },
    }
  }

  registerService<T>(pluginId: string, serviceId: string, impl: T): Disposable {
    const key = serviceId.trim()
    if (!key) {
      throw new Error('serviceId is required')
    }
    const entry: RegisteredServiceEntry = { serviceId: key, pluginId, impl }
    this.services.set(key, entry)
    this.emitServiceChange()
    return {
      dispose: () => {
        if (this.services.get(key) === entry) {
          this.services.delete(key)
          this.emitServiceChange()
        }
      },
    }
  }

  getService<T = unknown>(serviceId: string): T | undefined {
    const entry = this.services.get(serviceId.trim())
    return entry ? (entry.impl as T) : undefined
  }

  listServices(): Array<{ serviceId: string; pluginId: string }> {
    return Array.from(this.services.values()).map(({ serviceId, pluginId }) => ({
      serviceId,
      pluginId,
    }))
  }

  subscribeServices(listener: () => void): () => void {
    this.serviceListeners.add(listener)
    return () => {
      this.serviceListeners.delete(listener)
    }
  }

  getServiceVersion(): number {
    return this.serviceVersion
  }

  private emitServiceChange(): void {
    this.serviceVersion += 1
    for (const listener of this.serviceListeners) {
      listener()
    }
  }

  clear(): void {
    this.hostSubscribers.clear()
    this.pluginSubscribers.clear()
    if (this.services.size > 0) {
      this.services.clear()
      this.emitServiceChange()
    }
  }
}

export const pluginEventBus = new PluginEventBus()
