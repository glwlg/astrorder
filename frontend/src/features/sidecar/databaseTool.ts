import type { Session } from '../../domain/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
export {
  buildSqlSnippetFilename,
  extractSqlFileName,
  extractSqlLine,
  isSqlFilePath,
  isSqlLanguage,
  stripSqlLocationSuffix,
} from './viewers/dbx/sqlFormats'

export const DATABASE_TOOL_SLOT = 'database-tool'

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

class DatabaseToolRegistry {
  private providers: DatabaseToolProvider[] = []

  register(provider: DatabaseToolProvider) {
    const idx = this.providers.findIndex((p) => p.id === provider.id)
    if (idx >= 0) {
      this.providers[idx] = provider
    } else {
      this.providers.push(provider)
    }
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

export function getActiveDatabaseTool(): DatabaseToolProvider | null {
  return databaseToolRegistry.getActiveProvider()
}

export function hasActiveDatabaseTool(): boolean {
  return getActiveDatabaseTool() !== null
}

export function useHasDatabaseTool(): boolean {
  return usePluginSettingsStore((state) => databaseToolRegistry.getActiveProvider(state.enabledPlugins) !== null)
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
