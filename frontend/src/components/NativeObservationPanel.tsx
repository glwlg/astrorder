import { Badge, Button, Group, Stack, Text } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { useState } from 'react'
import { api } from '../api/client'
import { LazyDetails } from './LazyDetails'

export function NativeObservationPanel({ agentId, sessionId }: { agentId: string; sessionId?: string }) {
  const query=useQuery({ queryKey:['astrorder','observations',agentId,sessionId],queryFn:()=>api.getObservations(agentId,sessionId),refetchInterval:10000,retry:false })
  const [busy,setBusy]=useState(false)
  const state=query.data?.status
  const install=async()=>{
    setBusy(true)
    try { await api.installObserver(agentId); await query.refetch(); notifications.show({message:'观察钩子已安装。请在对应 Codex 的 /hooks 中审核信任；不会绕过原生安全检查。'}) }
    catch { notifications.show({color:'red',message:'观察钩子安装未确认，已有配置保持保留。请检查连接。'}) }
    finally {setBusy(false)}
  }
  return <Stack gap="xs" aria-label="原生观察钩子">
    <Group justify="space-between"><Text size="sm" fw={600}>原生端观察</Text><Badge variant="light" color={state?.trusted?'teal':'gray'}>{state?.trusted?'已信任':state?.needs_review?'待原生信任':state?.installed?'已安装 · 状态待确认':'未确认安装'}</Badge></Group>
    <Text size="xs" c="dimmed">记录会话、工具、子代理及轮次活动；完成和待审批可通知。不采集提示词、工具参数或输出，不代为审批。</Text>
    {state?.needs_review && <Text size="sm">在此环境的 Codex 输入 <code>/hooks</code>，审核并信任 Astrorder passive observation。已有客户端可能需要重新加载钩子。</Text>}
    <Group><Button size="compact-xs" variant="subtle" loading={busy} onClick={()=>void install()}>{state?.installed?'更新观察钩子':'安装观察钩子'}</Button><Text size="xs" c="dimmed">{state?.last_event_at?`最近收到 ${new Date(state.last_event_at*1000).toLocaleTimeString()}`:'尚未收到原生钩子事件'}</Text></Group>
    <LazyDetails summary={`观察记录 · ${query.data?.items.length || 0}`}>
      <Stack gap={6}>{query.data?.items.map(row=><Text size="xs" key={row.id}><time>{new Date(row.observed_at*1000).toLocaleTimeString()}</time> · {row.label}{row.tool_name?` · ${row.tool_name}`:''}</Text>)}</Stack>
    </LazyDetails>
  </Stack>
}
