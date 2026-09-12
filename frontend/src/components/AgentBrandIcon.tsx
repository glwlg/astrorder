import { IconBrandOpenai, IconRobot } from '@tabler/icons-react'
import type { AgentKind } from '../domain/types'

export function AgentBrandIcon({ kind, size = 16 }: { kind?: AgentKind; size?: number }) {
  if (kind === 'hermes') return <img src="/hermes-logo.png" alt="" aria-hidden="true" draggable={false} style={{ width: size, height: size, objectFit: 'contain' }} />
  if (kind === 'codex') return <IconBrandOpenai size={size} />
  return <IconRobot size={size} />
}
