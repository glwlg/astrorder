import { IconSparkles } from '@tabler/icons-react'
import { Center, Paper, Stack, Text, Title } from '@mantine/core'

export function EmptyState({
  title,
  description,
  icon = <IconSparkles size={30} stroke={1.5} />,
}: {
  title: string
  description: string
  icon?: React.ReactNode
}) {
  return (
    <Center className="empty-state" mih={220} p="xl">
      <Paper className="empty-state-card" withBorder radius="lg" p="xl">
        <Stack align="center" gap="xs">
          <span className="empty-state-icon" aria-hidden="true">{icon}</span>
          <Title order={3} size="h4">{title}</Title>
          <Text c="dimmed" ta="center" maw={440}>{description}</Text>
        </Stack>
      </Paper>
    </Center>
  )
}
