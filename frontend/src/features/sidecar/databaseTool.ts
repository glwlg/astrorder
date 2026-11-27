import { useSyncExternalStore } from 'react'
import { authenticatedApiFetch } from '../../api/client'
import type { Session } from '../../domain/types'
import type { Disposable } from '../extensions/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
export {
  buildSqlSnippetFilename,
  extractSqlFileName,
  extractSqlLine,
  isSqlFilePath,
  isSqlLanguage,
  stripSqlLocationSuffix,
} from './sqlFormats'

export const DATABASE_TOOL_SLOT = 'database-tool'
export const DBX_PLUGIN_ID = 'dbx-viewer'

export interface OpenSqlFileParams {
  path: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  content?: string
  line?: number
  forceNew?: boolean
}

export interface OpenSqlSnippetParams {
  sql: string
  filename?: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  forceNew?: boolean
}

export interface DatabaseToolProvider {
  id: string
  implementationName: string
  mediaType: string
  openWorkbench: (sessionId?: string, agentId?: string, connectionId?: string) => void
  openSqlFile: (params: OpenSqlFileParams) => Promise<void>
  openSqlSnippet: (params: OpenSqlSnippetParams) => void
}

export async function readSqlText(
  path: string,
  sessionId?: string,
  connectionId?: string,
): Promise<string> {
  const query = new URLSearchParams({ path })
  if (sessionId) query.set('session_id', sessionId)
  if (connectionId) query.set('connection_id', connectionId)
  const res = await authenticatedApiFetch(`/api/v1/files/raw?${query.toString()}`, {
    headers: { Accept: 'text/plain, application/sql, */*' },
  })
  if (!res.ok) {
    throw new Error(`无法读取 SQL 文件（HTTP ${res.status}）`)
  }
  const bytes = new Uint8Array(await res.arrayBuffer())
  const encoding =
    bytes[0] === 0xff && bytes[1] === 0xfe
      ? 'utf-16le'
      : bytes[0] === 0xfe && bytes[1] === 0xff
        ? 'utf-16be'
        : 'utf-8'
  return new TextDecoder(encoding).decode(bytes)
}

class DatabaseToolRegistry {
  private providers: DatabaseToolProvider[] = []
  private listeners = new Set<() => void>()
  private version = 0

  private emit(): void {
    this.version += 1
    for (const listener of this.listeners) {
      listener()
    }
  }

  register(provider: DatabaseToolProvider): Disposable {
    const idx = this.providers.findIndex((p) => p.id === provider.id)
    if (idx >= 0) {
      this.providers[idx] = provider
    } else {
      this.providers.push(provider)
    }
    this.emit()
    return {
      dispose: () => {
        const currentIdx = this.providers.findIndex((p) => p === provider)
        if (currentIdx >= 0) {
          this.providers.splice(currentIdx, 1)
          this.emit()
        }
      },
    }
  }

  unregister(id: string): boolean {
    const idx = this.providers.findIndex((p) => p.id === id)
    if (idx < 0) return false
    this.providers.splice(idx, 1)
    this.emit()
    return true
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  getSnapshotVersion(): number {
    return this.version
  }

  getAllProviders(): DatabaseToolProvider[] {
    return [...this.providers]
  }

  getActiveProvider(enabledPlugins?: Record<string, boolean>): DatabaseToolProvider | null {
    const map = enabledPlugins ?? usePluginSettingsStore.getState().enabledPlugins
    for (const provider of this.providers) {
      if (map[provider.id] !== false) {
        return provider
      }
    }
    return null
  }
}

export const databaseToolRegistry = new DatabaseToolRegistry()

export function useDatabaseToolRegistryVersion(): number {
  return useSyncExternalStore(
    (onStoreChange) => databaseToolRegistry.subscribe(onStoreChange),
    () => databaseToolRegistry.getSnapshotVersion(),
    () => databaseToolRegistry.getSnapshotVersion(),
  )
}

export function getActiveDatabaseTool(): DatabaseToolProvider | null {
  return databaseToolRegistry.getActiveProvider()
}

export function hasActiveDatabaseTool(): boolean {
  return getActiveDatabaseTool() !== null
}

export function useHasDatabaseTool(): boolean {
  const version = useDatabaseToolRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((state) => state.enabledPlugins)
  void version
  return databaseToolRegistry.getActiveProvider(enabledPlugins) !== null
}

export function openDatabaseWorkbench(sessionId?: string, agentId?: string, connectionId?: string): boolean {
  const provider = getActiveDatabaseTool()
  if (!provider) return false
  provider.openWorkbench(sessionId, agentId, connectionId)
  return true
}

export async function openSqlFileInDatabaseTool(params: OpenSqlFileParams): Promise<boolean> {
  const provider = getActiveDatabaseTool()
  if (!provider) return false
  await provider.openSqlFile(params)
  return true
}

export function openSqlSnippetInDatabaseTool(params: OpenSqlSnippetParams): boolean {
  const provider = getActiveDatabaseTool()
  if (!provider) return false
  provider.openSqlSnippet(params)
  return true
}
