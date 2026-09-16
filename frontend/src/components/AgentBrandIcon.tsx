import { IconBrandOpenai, IconRobot } from '@tabler/icons-react'
import type { AgentKind } from '../domain/types'

export function agentKindLabel(kind?: AgentKind): string {
  if (kind === 'hermes') return 'Hermes'
  if (kind === 'codex') return 'Codex'
  if (kind === 'grok') return 'Grok Build'
  if (!kind) return 'Agent'
  return kind.split(/[._-]+/).map(part => part.charAt(0).toUpperCase() + part.slice(1)).join(' ')
}

export function AgentBrandIcon({ kind, size = 16 }: { kind?: AgentKind; size?: number }) {
  if (kind === 'hermes') return <img src="/hermes-logo.png" alt="" aria-hidden="true" draggable={false} style={{ width: size, height: size, objectFit: 'contain' }} />
  if (kind === 'grok') return <img src="/grok-logo.png" alt="" aria-hidden="true" draggable={false} style={{ width: size, height: size, objectFit: 'contain' }} />
  if (kind === 'codex') return <IconBrandOpenai size={size} />
  return <IconRobot size={size} />
}
