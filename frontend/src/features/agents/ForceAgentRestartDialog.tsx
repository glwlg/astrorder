import { Button, Group, Text, Title } from '@mantine/core'
import { useId, useLayoutEffect, useRef } from 'react'
import './ForceAgentRestartDialog.css'

export function ForceAgentRestartDialog({ environment, onConfirm, onClose }: {
  environment: string
  onConfirm: () => void
  onClose: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const title = useId()
  const detail = useId()
  useLayoutEffect(() => {
    if (!dialog.current?.open) dialog.current?.showModal()
  }, [])

  return (
    <dialog
      ref={dialog}
      className="force-agent-restart-dialog"
      aria-labelledby={title}
      aria-describedby={detail}
      onClose={(event) => {
        const confirmed = event.currentTarget.returnValue === 'restart'
        event.currentTarget.returnValue = ''
        onClose()
        if (confirmed) onConfirm()
      }}
    >
      <form method="dialog">
        <Title id={title} order={2} size="h5">强制重启 Antigravity</Title>
        <Text id={detail} size="sm" mt="sm">
          {environment} 中此 Agent 的运行中任务和待处理审批会被中断。
        </Text>
        <Group justify="flex-end" mt="lg">
          <Button type="submit" value="cancel" variant="default" autoFocus>取消</Button>
          <Button type="submit" value="restart" color="red">强制重启</Button>
        </Group>
      </form>
    </dialog>
  )
}
