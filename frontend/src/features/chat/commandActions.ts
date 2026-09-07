import type { Command, CommandPayload, Session } from '../../domain/types'

export interface SubmitBrowserCommandInput {
  commandId: string
  session: Session
  text: string
  files: File[]
  action: CommandPayload['action']
  targetId?: string | null
  uploadAttachment: (file: File) => Promise<import('../../domain/types').Attachment>
  createCommand: (payload: CommandPayload) => Promise<Command>
}

export interface SubmittedBrowserCommand {
  command: Command
  payload: CommandPayload
  attachments: import('../../domain/types').Attachment[]
}

/**
 * Submit exactly one browser command. There is deliberately no retry here:
 * an ambiguous HTTP result must stay unknown until an event or refresh says
 * what happened. A caller creates a new id only for a new intentional action.
 */
export async function submitBrowserCommand({
  commandId,
  session,
  text,
  files,
  action,
  targetId = null,
  uploadAttachment,
  createCommand,
}: SubmitBrowserCommandInput): Promise<SubmittedBrowserCommand> {
  const attachments = []
  for (const file of files) {
    attachments.push(await uploadAttachment(file))
  }

  const payload: CommandPayload = {
    id: commandId,
    agent_id: session.agent_id,
    session_id: session.id,
    action,
    text,
    attachment_ids: attachments.map((item) => item.id),
    target_id: targetId,
  }
  const command = await createCommand(payload)
  return { command, payload, attachments }
}
