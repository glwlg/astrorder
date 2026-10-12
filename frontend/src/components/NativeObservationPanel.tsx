import { Button, Group, Text } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { useState } from 'react'
import { api } from '../api/client'

export function NativeObservationPanel({ agentId, sessionId }: { agentId: string; sessionId?: string }) {
  const query=useQuery({ queryKey:['astrorder','observations',agentId,sessionId],queryFn:()=>api.getObservations(agentId,sessionId),refetchInterval:10000,retry:false })
  const [busy,setBusy]=useState(false)
  const [outcome,setOutcome]=useState<{trusted:boolean;needsReview:boolean}|null>(null)
  const state=query.data?.status
  const install=async()=>{
    setBusy(true)
    try {
      const result=await api.installObserver(agentId)
      setOutcome({trusted:!!result.trusted,needsReview:!!result.needs_review})
      await query.refetch()
      if (result.trusted) notifications.show({message:'观察钩子已配置并由 Codex 信任，无需手动确认。'})
      else notifications.show({color:'yellow',message:`观察钩子已配置，但信任未完成：${result.trust_error||'请在对应 Codex 的 /hooks 中确认'}`})
    }
    catch (error) { notifications.show({color:'red',message:`观察钩子配置失败：${error instanceof Error?error.message:'请检查连接'}`}) }
    finally {setBusy(false)}
  }
  return (
    <div className="native-observation-box" aria-label="原生观察钩子">
      <Group justify="space-between" align="center">
        <Group gap={8} align="center">
          <Text size="xs" fw={600} style={{ letterSpacing: '0.02em' }}>原生端被动观察</Text>
          <Text size="xs" c="dimmed">·</Text>
          <Text size="xs" c="dimmed">
            {state?.last_event_at ? `最近活动 ${new Date(state.last_event_at * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}` : '暂无事件'}
          </Text>
        </Group>
        <Group gap="xs" align="center">
          <span className={`observation-status-pill ${(outcome?.trusted ?? state?.trusted) ? 'is-trusted' : ''}`}>
            <span className="status-indicator-dot" />
            <span>{(outcome?.trusted ?? state?.trusted) ? '已信任' : (outcome?.needsReview ?? state?.needs_review) ? '待审核' : state?.installed ? '已就绪' : '未配置'}</span>
          </span>
          <Button size="compact-xs" variant="subtle" color="gray" loading={busy} onClick={() => void install()}>
            {state?.installed ? '更新' : '配置'}
          </Button>
        </Group>
      </Group>
      {(outcome?.needsReview ?? state?.needs_review) && (
        <Text size="xs" c="yellow" mt={6}>
          请在宿主 Codex 输入 <code>/hooks</code> 确认授权。
        </Text>
      )}
    </div>
  )
}
