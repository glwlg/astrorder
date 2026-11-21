import { lazy, type ComponentType } from 'react'
import type { AgentKind } from '../../domain/types'

const AgyAccountsPage = lazy(() =>
  import('./AgyAccountsPage').then((mod) => ({ default: mod.AgyAccountsPage })),
)

export const accountPlatforms: readonly { kind: AgentKind; path: string; Page?: ComponentType }[] = [
  { kind: 'antigravity', path: '', Page: AgyAccountsPage },
  { kind: 'codex', path: 'codex' },
  { kind: 'hermes', path: 'hermes' },
  { kind: 'grok', path: 'grok' },
]
