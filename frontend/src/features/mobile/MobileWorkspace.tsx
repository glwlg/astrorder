import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useMantineColorScheme } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { IconSun, IconMoon, IconDeviceDesktop, IconBell, IconRefresh, IconPlayerPlay, IconInfoCircle, IconNotes, IconPlayerStop, IconSend, IconMessageCircle, IconCheck, IconX, IconMicrophone, IconPlus, IconDotsVertical, IconCpu, IconLoader2, IconPlugConnected } from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { api } from '../../api/client'
import type { Session, Task } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { requestNotificationPermission } from '../../domain/notifications'
import { selectApprovals, selectCommands, selectProjects, selectSessions, selectTasks, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { useSessionModel } from '../../hooks/useSessionModel'
import { useSessionOrder, notifySessionSubmitted } from '../../hooks/useSessionOrder'
import { buildProjectGroups } from '../../components/sessionRailModel'
import type { ProjectGroup } from '../../components/sessionRailModel'
import { NewSessionDialog } from '../../components/NewSessionDialog'
import { AgentSessionFilter, matchesAgent } from '../../components/AgentSessionFilter'
import { adjacentOpenSession, isSessionOpen } from '../../components/sessionVisibility'
import { PROJECT_ORDER_KEY, readProjectOrder, reconcileProjectOrder } from '../../components/projectOrder'
import { loadProjectAppearance } from '../../components/projectAppearance'
import { MobileSessionDrawer } from './MobileSessionDrawer'
import { VoiceInputSheet } from '../chat/VoiceInputSheet'
import { MobileTranscript, type MessageActionAnchor } from './MobileTranscript'
import { MobileMessageMenu } from './MobileMessageMenu'
import { EnvironmentConnections } from '../agents/EnvironmentConnections'
import { MobileApprovals } from './MobileApprovals'
import { AgentKindBadge, SessionRuntimeFacts } from '../../components/SessionRuntimeFacts'
import { MobileAttachmentPreview } from './MobileAttachmentPreview'
import { MobileOutbox } from './mobileOutbox'
import { mobileOutboxStorage } from './mobileOutboxStorage'
import './mobile.css'
import './mobilePolish.css'

const messageError = (error: unknown) => error instanceof Error ? error.message : '操作未确认，请检查连接。'
const queueLabels = { queued: '排队待发', submitting: '发送中', received: '等待原生确认', accepted: '已接受，等待本轮结束', running: '执行中', unknown: '结果未确认，未自动重发', failed: '未发送成功，内容已保留', cancelled: '已取消', completed: '已完成' }

export function MobileWorkspace() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const rawSessions = useAstrorderStore(useShallow(selectSessions))
  const sessions = useSessionOrder(rawSessions)
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agents = useAstrorderStore(state => state.agents)
  const connection = useAstrorderStore(state => state.connection)
  const openState = useQuery({ queryKey: ['astrorder', 'open-sessions'], queryFn: api.getOpenSessions, staleTime: 10000, retry: false })
  const navigableSessions = useMemo(() => {
    const openKeys = new Set(openState.data?.items.map(s => scopeKey(s.agent_id, s.id)))
    return sessions.map(s => openState.data?.known_agent_ids.includes(s.agent_id) ? { ...s, is_open: openKeys.has(scopeKey(s.agent_id, s.id)) } : s)
  }, [sessions, openState.data])
  const route = new URLSearchParams(location.search)
  const routeId = location.pathname.match(/\/chat\/([^/]+)$/)?.[1]
  const selected = sessions.find(s => s.id === (routeId ? decodeURIComponent(routeId) : '') && s.agent_id === route.get('agent_id')) || null
  const select = (session: Session) => { navigate(`/mobile/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`); setSheet(null) }
  useEffect(() => { if (!routeId && sessions.length) navigate(`/mobile/chat/${encodeURIComponent(sessions[0].id)}?agent_id=${encodeURIComponent(sessions[0].agent_id)}`, { replace: true }) }, [routeId, sessions, navigate])
  const resources = useSessionResources(selected, true)
  const sessionModel = useSessionModel(selected)
  const messages = resources.visibleMessages
  const tasks = useAstrorderStore(useShallow(state => selected ? selectTasks(state, selected.agent_id, selected.id) : []))
  const commands = useAstrorderStore(useShallow(state => selected ? selectCommands(state, selected.agent_id, selected.id) : []))
  const activeTasks = tasks.filter(task => task.status === 'running' || task.status === 'waiting_approval')
  const approvals = useAstrorderStore(useShallow(state => selected ? selectApprovals(state, selected.agent_id, selected.id) : []))
  const nativeBusy = selected?.status === 'running' || selected?.status === 'waiting_approval' || activeTasks.length > 0
  const [sheet, setSheet] = useState<'sessions' | 'status' | 'task' | 'models' | 'queue' | 'connections' | null>(null)
  const [messageAction, setMessageAction] = useState<(MessageActionAnchor & { sessionKey: string }) | null>(null)
  const closeMessageMenu = useCallback(() => setMessageAction(null), [])
  const [task, setTask] = useState<Task | null>(null)
  const [voice, setVoice] = useState(false)
  const [image, setImage] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, string>>(() => { try { return JSON.parse(localStorage.getItem('astrorder:mobile-drafts') || '{}') } catch { return {} } })
  const key = selected ? scopeKey(selected.agent_id, selected.id) : ''
  useEffect(closeMessageMenu, [key, closeMessageMenu])
  const text = drafts[key] || ''
  const setText = (value: string) => setDrafts(prev => { const next = { ...prev, [key]: value }; try { localStorage.setItem('astrorder:mobile-drafts', JSON.stringify(next)) } catch {} return next })
  const [filesBySession, setFiles] = useState<Record<string, File[]>>({})
  const files = filesBySession[key] || []
  const updateFiles = (value: File[]) => setFiles(prev => ({ ...prev, [key]: value }))
  const [quotes, setQuotes] = useState<Record<string, string>>({})
  const quote = quotes[key] || ''
  const setQuote = (value: string) => setQuotes(prev => ({ ...prev, [key]: value }))
  const [outbox] = useState(() => new MobileOutbox(mobileOutboxStorage, {
    upload: api.uploadAttachment,
    send: async payload => { const result = await api.createCommand(payload); useAstrorderStore.getState().mergeCommands([result]); return result },
  }))
  const queue = useSyncExternalStore(outbox.subscribe, outbox.snapshot)
  const [outboxReady, setOutboxReady] = useState(false)
  useEffect(() => { let mounted = true; void outbox.load().then(() => { if (mounted) setOutboxReady(true) }).catch(error => notifications.show({ message: messageError(error), color: 'red' })); return () => { mounted = false } }, [outbox])
  const pending = queue.filter(row => row.payload.agent_id === selected?.agent_id && row.payload.session_id === selected?.id)
  const busy = nativeBusy || pending.some(row => ['submitting', 'accepted', 'running'].includes(row.state))
  const canDispatch = !nativeBusy && connection === 'connected' && agents[selected?.agent_id || '']?.status === 'ready' && resources.commands.isSuccess
  useEffect(() => { if (outboxReady) void outbox.reconcile(commands, sessions).catch(error => notifications.show({ message: messageError(error), color: 'red' })) }, [outbox, outboxReady, commands, sessions])
  useEffect(() => {
    if (outboxReady && canDispatch && selected && queue.some(row => row.state === 'queued' && row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id)) {
      void outbox.flush(selected.agent_id, selected.id).catch(error => notifications.show({ message: messageError(error), color: 'red' }))
    }
  }, [outbox, outboxReady, canDispatch, selected, queue])
  const sending = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const [filter, setFilter] = useState('all')
  const [agentFilter, setAgentFilter] = useState('all')
  const [createOpened, setCreateOpened] = useState(false)
  const [createProject, setCreateProject] = useState<ProjectGroup | null>(null)
  const [search, setSearch] = useState('')

  const [pins, setPins] = useState<Record<string, boolean>>(() => { try { return JSON.parse(localStorage.getItem('astrorder_pinned_sessions') || '{}') } catch { return {} } })
  const appearance = useMemo(loadProjectAppearance, [])
  const [order, setOrder] = useState(readProjectOrder)
  const allGroups = useMemo(() => buildProjectGroups(sessions, agents, projects), [sessions, agents, projects])
  const stableOrder = useMemo(() => reconcileProjectOrder(order, allGroups.map(p => p.key)), [order, allGroups])
  useEffect(() => { if (stableOrder.length !== order.length) { setOrder(stableOrder); localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(stableOrder)) } }, [stableOrder, order])
  const visible = navigableSessions.filter(s => {
    if (!matchesAgent(s, agents, agentFilter)) return false
    if (filter === 'pinned' && !pins[scopeKey(s.agent_id, s.id)]) return false
    if (filter === 'open' && !isSessionOpen(s)) return false
    if (filter === 'recent' && Date.now() - Date.parse(s.updated_at) > 86400000) return false
    if (filter === 'unread') return (s as Session & { unread?: boolean }).unread === true
    return true
  })
  const grouped = buildProjectGroups(visible, agents, projects, search.trim().toLowerCase()).filter(project => (filter === 'all' && agentFilter === 'all') || project.sessions.length > 0)
  const groups = stableOrder.flatMap(id => grouped.filter(p => p.key === id))
  const notify = (message: string, color = 'blue') => notifications.show({ message, color })
  const [modelSearch, setModelSearch] = useState('')
  const [modelChoices, setModelChoices] = useState<{ provider: string; model: string; label: string }[]>([])
  const [modelLoading, setModelLoading] = useState(false)

  const [modelSelection, setModelSelection] = useState<{ provider: string; model: string; label: string } | null>(null)
  const openModels = async () => {
    if (!selected) return
    setSheet('models'); setModelLoading(true); setModelChoices([]); setModelSearch(''); setModelSelection(null)
    try { setModelChoices((await api.getSessionModels(selected.id, selected.agent_id)).items) }
    catch (error) { notify(messageError(error), 'red') }
    finally { setModelLoading(false) }
  }
  const chooseModel = async (provider: string, model: string) => {
    if (!selected || modelLoading) return
    setModelLoading(true)
    try { const result = await sessionModel.change(provider, model); setSheet(null); notify(result.deferred ? '原生运行时已确认，下轮使用新模型' : '原生模型切换已确认') }
    catch (error) { notify(messageError(error), 'red') }
    finally { setModelLoading(false) }
  }
  const refresh = async () => { await queryClient.invalidateQueries({ queryKey: ['astrorder'] }); notify('进度已对齐最新状态') }
  useEffect(() => {
    const resume = () => { if (document.visibilityState === 'visible') void queryClient.invalidateQueries({ queryKey: ['astrorder'] }) }
    window.addEventListener('online', resume); document.addEventListener('visibilitychange', resume)
    return () => { window.removeEventListener('online', resume); document.removeEventListener('visibilitychange', resume) }
  }, [queryClient])
  const send = async (quickText?: string) => {
    if (!selected || sending.current) return
    const body = quickText ?? (quote ? quote.split('\n').map(line => `> ${line}`).join('\n') + '\n\n' + text : text)
    const chosenFiles = quickText ? [] : files
    if (!body.trim() && !chosenFiles.length) return
    sending.current = true; setSubmitting(true)
    try {
      if (!outboxReady) await outbox.load()
      await outbox.enqueue({ id: crypto.randomUUID(), agent_id: selected.agent_id, session_id: selected.id, action: 'send', text: body, attachment_ids: [], target_id: null }, chosenFiles)
      notifySessionSubmitted(selected)
      if (!quickText) { setText(''); updateFiles([]); setQuote('') }
      if (!canDispatch) notify('已保存到待发队列')
    } catch (error) {
      notify(messageError(error), 'red')
    } finally { sending.current = false; setSubmitting(false) }
  }
  const flush = async () => {
    if (!selected) return
    try {
      const result = await api.getCommands(selected.id, selected.agent_id)
      useAstrorderStore.getState().mergeCommands(result.items)
      await outbox.reconcile(result.items, sessions)
      if (canDispatch) await outbox.flush(selected.agent_id, selected.id)
      else notify('会话正在运行或连接未就绪，队列已保留')
    } catch (error) { notify(messageError(error), 'red') }
  }
  const stop = async (target?: Task) => {
    if (!selected) return
    if (target && !window.confirm('中断当前会话以停止该任务？这也会停止本轮其他活动。')) return
    const targetId = selected.id
    try { const result = await api.createCommand({ id: crypto.randomUUID(), agent_id: selected.agent_id, session_id: selected.id, action: 'stop', text: '', attachment_ids: [], target_id: targetId }); useAstrorderStore.getState().mergeCommands([result]); if (result.state === 'failed' || result.state === 'unknown') throw new Error(result.error || '停止操作未确认') } catch (error) { notify(messageError(error), 'red') }
  }
  const stopAll = async () => { if (window.confirm('终止当前会话的全部运行任务？')) await stop() }
  const switchSession = (direction: number) => { const next = adjacentOpenSession(navigableSessions, selected, direction); if (next) select(next) }
  const copy = async (value: string) => { try { await navigator.clipboard.writeText(value); notify('已复制') } catch { notify('浏览器未允许复制，请使用选择文字') } }
  return <div className="mobile-workspace" data-mobile-shell="independent">
    {createOpened && <NewSessionDialog agents={agents} project={createProject} onClose={() => setCreateOpened(false)} onCreated={session => { setAgentFilter('all'); setFilter('all'); setSearch(''); select(session) }} />}
    <header className="m-header"><div className="m-brand"><img className="m-brand-mark" src="/favicon.svg" alt="" /><strong>星序</strong><span className={`m-dot ${connection === 'connected' ? 'online' : ''}`} aria-label={connection === 'connected' ? '已连接' : '连接中'} /></div><div className="m-header-actions">
      <button aria-label="连接管理" title="连接管理" onClick={() => setSheet('connections')}><IconPlugConnected size={19} /></button>
      <button aria-label="切换主题" onClick={() => setColorScheme(colorScheme === 'auto' ? 'light' : colorScheme === 'light' ? 'dark' : 'auto')}>{colorScheme === 'auto' ? <IconDeviceDesktop size={19} /> : colorScheme === 'light' ? <IconSun size={19} /> : <IconMoon size={19} />}</button>
      <button aria-label="通知设置" onClick={async () => notify(`通知权限：${await requestNotificationPermission()}`)}><IconBell size={19} /></button><button aria-label="刷新" onClick={() => void refresh()}><IconRefresh size={19} /></button>
    </div></header>
    <main className="m-main">{selected ? <>
      <div className="m-card-head"><div><h1>{selected.title || selected.id}</h1><small><AgentKindBadge agent={agents[selected.agent_id]} /> {agents[selected.agent_id]?.name || selected.agent_id}</small></div><button aria-label="会话信息" onClick={() => setSheet('status')}><IconDotsVertical size={20} /></button></div>
      <MobileTranscript key={key} messages={messages} busy={busy} loadOlder={() => resources.messages.fetchNextPage()} hasOlder={!!resources.messages.hasNextPage} loadingOlder={resources.messages.isFetchingNextPage} onMessageAction={anchor => setMessageAction({ ...anchor, sessionKey: key })} onImage={setImage} onSwipe={switchSession} />
      {!!pending.length && <button className="m-queue-banner" onClick={() => setSheet('queue')}>消息队列 ({pending.length}) · {queueLabels[pending[0].state]}</button>}
      <div className="m-card-foot"><button aria-label="选择会话模型" title={sessionModel.label} onClick={() => void openModels()}><IconCpu size={14} /><span>{sessionModel.label}</span></button><span>左右滑动切换开放会话</span></div>
    </> : <div className="m-empty">从底部会话列表选择会话</div>}</main>
    <section className="m-composer-float">
      <div className="m-quick-actions"><button onClick={() => void send('继续')}><IconPlayerPlay size={14} />继续</button><button onClick={() => setSheet('status')}><IconInfoCircle size={14} />运行状态</button><button onClick={() => void send('帮我总结当前会话的最新进展与遗留事项')}><IconNotes size={14} />总结进展</button><button className="m-danger" onClick={() => void stopAll()}><IconPlayerStop size={14} />终止全部任务</button></div>
      {!!activeTasks.length && <div className="m-active-tasks">{activeTasks.map(item => <button key={item.id} onClick={() => { setTask(item); setSheet('task') }}><span className="m-dot online" />{item.title}</button>)}</div>}
      {!!approvals.length && <button className="m-queue-banner" onClick={() => setSheet('status')}>等待授权 · {approvals.length} 项</button>}
      {quote && <div className="m-quote"><span>{quote}</span><button aria-label="取消引用" onClick={() => setQuote('')}><IconX size={16} /></button></div>}
      {!!files.length && <div className="m-attachments">{files.map((file, i) => <MobileAttachmentPreview key={`${file.name}-${i}`} file={file} onOpen={setImage} onRemove={() => updateFiles(files.filter((_, n) => n !== i))} />)}</div>}
      <div className="m-composer"><button aria-label="添加附件" onClick={() => fileInput.current?.click()}><IconPlus size={22} /></button><input hidden ref={fileInput} type="file" multiple accept="image/*,audio/*,.pdf,.txt,.md,.json,.csv,.log" onChange={e => { updateFiles([...files, ...Array.from(e.target.files || [])]); e.target.value = '' }} />
        <textarea ref={textarea} aria-label="消息内容" placeholder="向 Agent 发送追加指令…" rows={1} value={text} onChange={e => { setText(e.target.value); e.target.style.height = '35px'; e.target.style.height = `${Math.min(140, Math.max(35, e.target.scrollHeight))}px` }} />
        {text && <button aria-label="清空输入内容" onClick={() => { setText(''); if (textarea.current) textarea.current.style.height = '35px' }}><IconX size={18} /></button>}
        <button aria-label="语音消息" onClick={() => setVoice(true)}><IconMicrophone size={21} /></button><button className="m-send" data-stop={busy && !text && !files.length} aria-label={busy && !text && !files.length ? '停止' : '发送'} disabled={submitting || !selected} onClick={() => busy && !text && !files.length ? void stop() : void send()}>{submitting ? <IconLoader2 className="m-spin" size={20} /> : busy && !text && !files.length ? <IconPlayerStop size={19} /> : <IconSend size={20} />}</button>
      </div>
    </section>
    <footer className="m-dock"><button aria-label="打开会话列表" onClick={() => { setSheet('sessions'); void openState.refetch() }}><span className="m-dock-icon"><IconMessageCircle size={21} /></span><span className="m-dot online" />{sessions.length} 个会话</button></footer>
    {messageAction?.sessionKey === key && <MobileMessageMenu anchor={messageAction} onClose={closeMessageMenu} onCopy={() => void copy(messageAction.text)} onQuote={() => { setQuote(messageAction.text); textarea.current?.focus({ preventScroll: true }) }} />}
    {sheet && <div className="m-backdrop" onClick={() => setSheet(null)}><section className={`m-sheet ${sheet === 'sessions' ? 'm-session-sheet' : ''}`} role="dialog" aria-label={sheet === 'sessions' ? '会话列表' : '详情'} onClick={e => e.stopPropagation()}><div className="m-handle" /><header><h2>{({ sessions: '会话', status: '运行状态', task: '任务详情', models: '选择模型', queue: '消息队列', connections: '连接管理' })[sheet]}</h2><button aria-label="关闭面板" onClick={() => setSheet(null)}><IconX size={20} /></button></header>
      {sheet === 'connections' && <div className="m-sheet-body"><EnvironmentConnections embedded /></div>}
      {sheet === 'sessions' && <div style={{ display: 'flex', gap: 8, padding: '8px 14px' }}><AgentSessionFilter agents={agents} value={agentFilter} onChange={setAgentFilter} /><button aria-label="新建会话" onClick={() => { setCreateProject(null); setCreateOpened(true) }}><IconPlus size={20} /></button></div>}
      {sheet === 'sessions' && <><input className="m-search" aria-label="搜索会话" placeholder="搜索会话" value={search} onChange={e => setSearch(e.target.value)} /><nav className="m-filters">{[['all','全部'],['unread','未读'],['open','开放中'],['pinned','置顶'],['recent','24小时']].map(([id,label]) => <button className={filter === id ? 'active' : ''} key={id} onClick={() => setFilter(id)}>{label}</button>)}</nav><MobileSessionDrawer groups={groups} pins={pins} selectedKey={key} appearance={appearance} onSelect={select} onCreate={project => { setCreateProject(project); setCreateOpened(true) }} onPin={s => { const next = { ...pins, [scopeKey(s.agent_id,s.id)]: !pins[scopeKey(s.agent_id,s.id)] }; setPins(next); localStorage.setItem('astrorder_pinned_sessions', JSON.stringify(next)) }} /></>}
      {sheet === 'status' && selected && <div className="m-sheet-body"><MobileApprovals session={selected} approvals={approvals} /><SessionRuntimeFacts session={selected} agent={agents[selected.agent_id]} /><button onClick={() => void copy(selected.id)}>复制会话 ID</button></div>}
      {sheet === 'task' && task && <div className="m-sheet-body"><p>{task.title}</p><p>{task.status}</p><pre>{task.command}</pre><button onClick={() => void copy(task.logs.map(log => log.text).join('\n'))}>复制日志</button><button onClick={e => { const pre=e.currentTarget.parentElement?.querySelector('.m-task-log'); if(pre) pre.scrollTop=pre.scrollHeight }}>跳到底部</button><pre className="m-task-log">{task.logs.map(log => log.text).join('\n')}</pre><button onClick={() => void stop(task)}>停止任务</button></div>}
      {sheet === 'models' && <div className="m-sheet-body m-model-panel"><input className="m-search" aria-label="搜索模型" placeholder="搜索模型或提供商" value={modelSearch} onChange={e => setModelSearch(e.target.value)} />{modelLoading && <p>正在处理原生模型请求…</p>}<div className="m-model-list">{modelChoices.filter(choice => choice.label.toLowerCase().includes(modelSearch.toLowerCase())).map(choice => <button className="m-model-choice" aria-pressed={modelSelection?.provider === choice.provider && modelSelection?.model === choice.model} key={`${choice.provider}/${choice.model}`} disabled={modelLoading} onClick={() => setModelSelection(choice)}><IconCpu size={17} /><span>{choice.label}</span>{modelSelection?.provider === choice.provider && modelSelection?.model === choice.model && <IconCheck size={17} />}</button>)}</div>{!modelLoading && !modelChoices.length && <p>原生运行时未返回可用模型。</p>}{modelSelection && <div className="m-model-confirm"><small>{modelSelection.label}</small><button aria-label="确认切换模型" disabled={modelLoading} onClick={() => void chooseModel(modelSelection.provider, modelSelection.model)}>{modelLoading ? '切换中…' : '确认切换模型'}</button></div>}</div>}
      {sheet === 'queue' && <div className="m-sheet-body">{pending.map(entry => <article className="m-outbox-entry" key={entry.payload.id} data-command-id={entry.payload.id}><p>{entry.payload.text || '附件消息'}</p><small>{queueLabels[entry.state]}</small><div>{[...entry.attachments, ...entry.files].map((file, i) => <span key={i}>{file.name} </span>)}</div>{['queued', 'failed', 'cancelled'].includes(entry.state) && <button onClick={() => void outbox.remove(entry.payload.agent_id, entry.payload.session_id, entry.payload.id).catch(error => notify(messageError(error), 'red'))}>移除待发消息</button>}{entry.state === 'failed' && <button onClick={() => void outbox.retry(entry.payload.agent_id, entry.payload.session_id, entry.payload.id).catch(error => notify(messageError(error), 'red'))}>重新发送</button>}</article>)}<button onClick={() => void flush()}>核对结果 / 发送下一条</button></div>}
    </section></div>}
    {image && <div className="m-lightbox" role="dialog" aria-label="图片预览" onClick={() => setImage(null)}><button aria-label="关闭图片"><IconX size={22} /></button><img src={image} alt="预览" /></div>}
    <VoiceInputSheet opened={voice} onClose={() => setVoice(false)} onCommit={(file, transcript) => { updateFiles([...files, file]); if (transcript) setText(text + (text ? '\n' : '') + transcript) }} />
  </div>
}
