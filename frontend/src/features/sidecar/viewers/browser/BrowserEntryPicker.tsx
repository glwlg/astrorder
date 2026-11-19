import { Button, Stack, Text } from '@mantine/core'
import { IconArrowUpRight } from '@tabler/icons-react'
import { useMemo } from 'react'
import { scopeKey } from '../../../../domain/semantics'
import { useAstrorderStore } from '../../../../state/store'
import { browserEntryUrls } from './browserEntries'

export function BrowserEntryPicker({ agentId, sessionId, onOpen }: { agentId: string; sessionId: string; onOpen: (url: string) => void }) {
  const messages = useAstrorderStore(state => state.messages[scopeKey(agentId, sessionId)])
  const urls = useMemo(() => browserEntryUrls(
    Object.values(messages || {}).sort((left, right) => left.created_at.localeCompare(right.created_at)).slice(-120).map(message => message.text),
  ), [messages])
  if (urls.length === 0) return null
  return (
    <Stack gap={7} mt={12} p="xs" style={{ width: 'min(100%, 440px)' }}>
      <Text size="xs" c="dimmed">会话中的地址</Text>
      {urls.map(url => (
        <Button
          type="button" key={url} variant="default" size="xs" onClick={() => onOpen(url)}
          rightSection={<IconArrowUpRight size={13} />} title={url}
          styles={{ label: { display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', textAlign: 'start', flex: 1 } }}
        >
          {url}
        </Button>
      ))}
    </Stack>
  )
}
