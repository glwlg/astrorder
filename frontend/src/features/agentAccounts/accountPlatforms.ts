import { lazy, type ComponentType } from 'react'
import type { AgentKind } from '../../domain/types'

const AgyAccountsPage = lazy(() =>
  import('./AgyAccountsPage').then((mod) => ({ default: mod.AgyAccountsPage })),
)
const CodexAccountsPage = lazy(() =>
  import('./AgyAccountsPage').then((mod) => ({ default: mod.CodexAccountsPage })),
)
const GrokAccountsPage = lazy(() =>
  import('./AgyAccountsPage').then((mod) => ({ default: mod.GrokAccountsPage })),
)

export const accountPlatforms: readonly { kind: AgentKind; path: string; Page?: ComponentType }[] = [
  { kind: 'antigravity', path: '', Page: AgyAccountsPage },
  { kind: 'codex', path: 'codex', Page: CodexAccountsPage },
  { kind: 'grok', path: 'grok', Page: GrokAccountsPage },
  { kind: 'hermes', path: 'hermes' },
]
