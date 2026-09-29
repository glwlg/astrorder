import type { Message } from '../domain/types'

// Hermes may persist its internal, model-only recall as part of a user row.
// Only strip the fenced block with Hermes' exact internal note, not user examples.
const injectedMemory = /(?:\r?\n)*<memory-context>\r?\n\[System note: The following is recalled memory context, NOT new user input\.[^\r\n]*\]\r?\n[\s\S]*?^<\/memory-context>(?:\r?\n)*/gm
const injectedPonytail = /(?:\r?\n)*PONYTAIL MODE ACTIVE — level: (?:lite|full|ultra)\r?\n\r?\n# Ponytail\r?\n[\s\S]*?\r?\nThe shortest path to done is the right path\.\s*$/

export function visibleHermesMessage(message: Message): Message {
  if (message.role !== 'user' || !/^(?:local|ssh)-hermes-/.test(message.agent_id)) return message
  const withoutPonytail = message.text.replace(injectedPonytail, '')
  const text = withoutPonytail.replace(injectedMemory, (block: string, offset: number) => {
    const before = withoutPonytail.slice(0, offset)
    const after = withoutPonytail.slice(offset + block.length)
    return before && after ? '\n\n' : ''
  })
  return text === message.text ? message : { ...message, text }
}
