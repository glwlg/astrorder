"""Passive native observations; never create messages, approve tools or claim turn ownership."""
import asyncio
import inspect
import re
import threading
import time
from pathlib import Path
from uuid import UUID

from sqlalchemy import select

from .models import EventRow
from .observer_io import read_spool, write_decision

LABELS={'SessionStart':'原生会话已打开','SessionEnd':'原生会话已关闭','UserPromptSubmit':'原生端提交了消息','PreToolUse':'工具开始执行','PostToolUse':'工具执行结束','PermissionRequest':'原生端等待审批','SubagentStart':'子代理已启动','SubagentStop':'子代理已结束','Stop':'原生轮次已结束','Interrupt':'原生轮次已中断'}


def reconcile_native_status(current: str | None, desired: str | None) -> str | None:
    """Promote to running from Hermes active_list, but never demote on a single
    poll. Hermes session.active_list drops a session during stream token gaps
    and tool-call handoffs, so clearing on an empty poll makes the rail flicker
    running/idle at ~1Hz. Demotion is handled by the frontend's 15s live-window
    and 120s stale-window, which decay naturally; here we only ever move toward
    running, never away from it on a transient empty poll.
    """
    status = current or "idle"
    if status == "error":
        return None
    if desired:
        return None if desired == status else desired
    # desired empty/None: leave status untouched (see docstring).
    return None


# Hysteresis for clearing a backend-promoted running state. A session promoted
# to running stays running until it has been absent from active_list for this
# long, so 1s-poll gaps during streaming can't bounce it.
RUNNING_CLEAR_GRACE_S = 90.0

def validate_observation(row):
    if not isinstance(row,dict) or row.get('event') not in LABELS: raise ValueError('Invalid observation')
    if not isinstance(row.get('id'),str) or not re.fullmatch(r'[a-f0-9]{32}(?:[a-f0-9]{32})?',row['id']): raise ValueError('Invalid event ID')
    sid=row.get('session_id'); UUID(sid)
    if not isinstance(row.get('observed_at'),(int,float)) or not time.time()-86400 <= row['observed_at'] <= time.time()+300: raise ValueError('Invalid observation time')
    data={key:row[key] for key in ('id','session_id','event','observed_at')}
    for key in ('turn_id','tool_call_id','tool_name','subagent_id'):
        value=row.get(key)
        if isinstance(value,str) and len(value)<=160 and all(ord(c)>=32 for c in value): data[key]=value
    if data['event']=='PermissionRequest' and row.get('approval_pending') is True:
        data['approval_pending']=True
        detail=row.get('detail')
        if isinstance(detail,str): data['detail']=detail[:2000]
    data['label']=LABELS[data['event']]
    data['notification']=data['event'] in ('Stop','Interrupt','PermissionRequest') and time.time()-data['observed_at']<60
    return data

def reply_preview(client, session_id, turn_id):
    if not turn_id: return ''
    try:
        response=client._request('thread/items/list', {'threadId':session_id,'cursor':None,'limit':20,'sortDirection':'desc'})
    except Exception:
        return ''
    for entry in response.get('data',[]):
        item=entry.get('item',{})
        if entry.get('turnId')==turn_id and item.get('type')=='agentMessage' and item.get('text'):
            text=re.sub(r'(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization)\s*[:=]\s*\S+',r'\1=[REDACTED]',item['text'])
            text=re.sub(r'(?i)\b(?:sk-[a-z0-9_-]{20,}|gh[pousr]_[a-z0-9]{20,})\b','[REDACTED]',text)
            text=re.sub(r'\s+',' ',text).strip()
            return text[:300]+('…' if len(text)>300 else '')
    return ''

class NativeObservers:
    def __init__(self, app):
        self.app=app
        self.states={}
        self.ack={}
        self.pending={}
        self.supported=set()
        self.lock=threading.RLock()
        self.stopping=asyncio.Event()

    def clients(self):
        env=self.app.state.environments
        return [env.codex,*env.remote.values()]

    def collect(self, client):
        if not client._home or client.state!='connected': return
        aid=client.agent_id
        home=client._home.as_posix()
        acknowledged=self.ack.get(aid,[])
        with self.lock: pending_ids=[key[2] for key in self.pending if key[0]==aid]
        if hasattr(client,'remote_json'):
            source='import json,re\nfrom pathlib import Path\n'+inspect.getsource(read_spool)+'\nprint(json.dumps(read_spool('+repr(home)+','+repr(acknowledged)+','+repr(pending_ids)+')))'
            response=client.remote_json(source)
        else:
            response=read_spool(Path(home),acknowledged,pending_ids)
        self.ack[aid]=[]
        state=self.states.setdefault(aid,{})
        state.update({'installed':response['installed'],'poll_ok':True})
        present=set(response.get('pending',[]))
        for raw in response['items']:
            try: data=validate_observation(raw)
            except (ValueError,TypeError,AttributeError):
                if isinstance(raw.get('id'),str): self.ack[aid].append(raw['id'])
                continue
            data['agent_id']=aid
            if self.app.state.store.get_session(aid,data['session_id']) is None:
                try:
                    native=client._request('thread/read',{'threadId':data['session_id'],'includeTurns':False})
                    if native.get('thread',{}).get('id')==data['session_id']:
                        client._record_thread(native['thread'])
                except (RuntimeError, OSError, ValueError):
                    self.states.setdefault(aid,{})['catalog_lookup_ok']=False
            # Unknown native IDs remain observations, not invented catalog entries.
            if data['event']=='Stop' and data['notification']:
                session=self.app.state.store.get_session(aid,data['session_id'])
                data['session_title']=session.get('title','') if session else ''
                try: data['preview']=reply_preview(client,data['session_id'],data.get('turn_id'))
                except (RuntimeError,OSError,ValueError): data['preview']=''
            if data.get('approval_pending'):
                present.add(data['id'])
                key=(aid,data['session_id'],data['id'])
                with self.lock:
                    if key not in self.pending:
                        approval={'id':f"observer-approval:{aid}:{data['id']}",'agent_id':aid,'session_id':data['session_id'],'target_id':data['id'],'title':'Codex 请求执行授权'+((' · '+data['tool_name']) if data.get('tool_name') else ''),'detail':data.get('detail') or data.get('tool_name') or '原生 Codex 操作等待授权','state':'pending','data':{'source':'codex-observer'}}
                        self.pending[key]=(client,approval)
                        if aid not in self.supported:
                            self.supported.add(aid); self.app.state.service._publish_capabilities(aid)
                        self.app.state.service._server_event('approval.upsert',agent_id=aid,session_id=data['session_id'],data=approval)
            event=self.app.state.store.append_event(event_id='observer-'+data['id'],event_type='native.observation',agent_id=aid,session_id=data['session_id'],data=data)
            self.app.state.service._publish(event)
            if not data.get('approval_pending'): self.ack[aid].append(data['id'])
            state['last_event_at']=max(state.get('last_event_at',0),data['observed_at'])
        with self.lock:
            for key in [key for key in self.pending if key[0]==aid and key[2] not in present]:
                _,approval=self.pending.pop(key)
                self.app.state.service._server_event('approval.upsert',agent_id=aid,session_id=key[1],data={**approval,'state':'resolved'})

    def matches(self,command):
        with self.lock: return (command['agent_id'],command['session_id'],command.get('target_id')) in self.pending

    async def submit(self,command):
        return await asyncio.to_thread(self.respond,command)

    def respond(self,command):
        aid,sid,rid=command['agent_id'],command['session_id'],command.get('target_id')
        with self.lock:
            record=self.pending.get((aid,sid,rid))
            if not record: return 'failed','审批已过期或不属于当前会话。'
            client,approval=record; home=client._home.as_posix()
            if hasattr(client,'remote_json'):
                source='import json,os,re\nfrom pathlib import Path\n'+inspect.getsource(write_decision)+'\nprint(json.dumps(write_decision('+repr(home)+','+repr(rid)+','+repr('allow' if command['action']=='approve' else 'deny')+')))'
                written=client.remote_json(source) is True
            else:
                written=write_decision(Path(home),rid,'allow' if command['action']=='approve' else 'deny')
            if not written: return 'failed','原生审批已过期；未发送决定。'
            self.pending.pop((aid,sid,rid),None)
            self.app.state.service._server_event('approval.upsert',agent_id=aid,session_id=sid,data={**approval,'state':'approved' if command['action']=='approve' else 'rejected'})
            updated=self.app.state.store.set_command_state(aid,sid,command['id'],'completed',None)
            self.app.state.service._server_event('command.upsert',agent_id=aid,session_id=sid,data=updated)
            return 'accepted',None

    def snapshot(self):
        with self.lock: return [dict(record[1]) for record in self.pending.values()]

    async def run(self):
        while not self.stopping.is_set():
            async def one(client):
                try: await asyncio.to_thread(self.collect,client)
                except (RuntimeError, OSError, ValueError):
                    self.states.setdefault(client.agent_id,{})['poll_ok']=False
            await asyncio.gather(*(one(c) for c in self.clients()))
            await asyncio.to_thread(self.app.state.service.hermes_approvals.poll)
            await asyncio.to_thread(self._reconcile_commands)
            await asyncio.to_thread(self._sync_hermes_activity)
            try: await asyncio.wait_for(self.stopping.wait(),timeout=1)
            except TimeoutError: pass

    def _reconcile_commands(self):
        connections = getattr(self.app.state, "connections", None)
        service = getattr(self.app.state, "service", None)
        store = getattr(self.app.state, "store", None)
        environments = getattr(self.app.state, "environments", None)
        if not service or not store:
            return
        active_commands = store.active_commands()
        for cmd in active_commands:
            if cmd.get("state") not in {"accepted", "running"}:
                continue
            aid = cmd.get("agent_id")
            sid = cmd.get("session_id")
            cid = cmd.get("id")
            cmd_created = cmd.get("created_at")

            # Check Hermes runtime
            runtime = connections.get_runtime_by_agent_id(aid) if connections else None
            # Check Codex runtime
            codex = environments.for_agent(aid) if environments else getattr(self.app.state, "codex", None)
            if codex and codex.agent_id != aid:
                codex = None

            if runtime is None and codex is None:
                continue

            try:
                # 1. If Codex reports session not active, or session itself in DB is idle/error
                session = store.get_session(aid, sid)
                if codex is not None:
                    is_active = hasattr(codex, "_active") and sid in codex._active
                    if not is_active and session and session.get("status") in {"idle", "error"}:
                        updated = store.set_command_state(aid, sid, cid, "completed", None)
                        service._server_event("command.upsert", agent_id=aid, session_id=sid, data=updated)
                        continue

                # 2. If messages contain assistant replies created after or equal to the command
                with store.session() as db:
                    from .store import MessageRow
                    latest_reply = db.scalars(
                        select(MessageRow).where(
                            MessageRow.agent_id == aid,
                            MessageRow.session_id == sid,
                            MessageRow.role == "assistant",
                        ).order_by(MessageRow.created_at.desc(), MessageRow.row_id.desc())
                    ).first()
                    if latest_reply and cmd_created:
                        msg_created = str(latest_reply.created_at).replace(" ", "T")
                        cmd_clean = str(cmd_created).replace("Z", "")
                        if msg_created >= cmd_clean or (session and session.get("status") == "idle"):
                            updated = store.set_command_state(aid, sid, cid, "completed", None)
                            service._server_event("command.upsert", agent_id=aid, session_id=sid, data=updated)
            except Exception:
                pass

    def _hermes_runtimes(self):
        connections = getattr(self.app.state, "connections", None)
        if not connections:
            return []
        runtimes = []
        local = getattr(connections, "local", None)
        if local is not None:
            runtimes.append(local)
        ssh = getattr(connections, "_ssh_runtimes", None) or {}
        if isinstance(ssh, dict):
            runtimes.extend(ssh.values())
        return runtimes

    def _sync_hermes_activity(self):
        from .native_sessions import active_native_session_status

        service = getattr(self.app.state, "service", None)
        store = getattr(self.app.state, "store", None)
        if not service or not store:
            return
        # Track when each session was last seen running so a transient empty
        # active_list poll can't clear it. Keyed by (agent_id, session_id).
        promoted_at = getattr(self, "_hermes_running_since", None)
        if promoted_at is None:
            promoted_at = self._hermes_running_since = {}
        now = time.time()
        for runtime in self._hermes_runtimes():
            rpc = getattr(runtime, "_rpc", None) or getattr(runtime, "rpc", None)
            agent_id = getattr(runtime, "_agent_id", None) or getattr(runtime, "agent_id", None)
            if callable(agent_id):
                agent_id = agent_id()
            # Connection liveness differs per runtime: the local controller has a
            # `_state` string ("connected"), the SSH runtime only exposes
            # `snapshot()["alive"]`. Accept either.
            state = getattr(runtime, "_state", None)
            alive = None
            if state is None or state != "connected":
                snapshot = runtime.snapshot() if callable(getattr(runtime, "snapshot", None)) else {}
                if isinstance(snapshot, dict):
                    if state is None:
                        state = snapshot.get("state")
                    alive = snapshot.get("alive")
            connected = (state == "connected") or (alive is True)
            if not connected or not callable(rpc) or not agent_id:
                continue
            try:
                active = active_native_session_status(rpc)
            except Exception:
                continue
            for session in store.list_sessions(agent_id):
                sid = session.get("id")
                if not isinstance(sid, str) or not sid:
                    continue
                source_sid = session.get("source_session_id") or sid
                desired = active.get(sid) or active.get(source_sid)
                key = (agent_id, sid)
                if desired == "running":
                    promoted_at[key] = now
                elif session.get("status") == "running":
                    # Only clear a backend-promoted running after it has been
                    # continuously absent from active_list past the grace window.
                    last_seen = promoted_at.get(key)
                    if last_seen is not None and (now - last_seen) < RUNNING_CLEAR_GRACE_S:
                        continue
                    # Past grace (or never promoted by us): clear it.
                    promoted_at.pop(key, None)
                    desired = "idle"
                next_status = reconcile_native_status(session.get("status"), desired)
                if not next_status:
                    continue
                updated = {**session, "status": next_status}
                canonical = store.upsert_session(updated)
                service._server_event("session.upsert", agent_id=agent_id, session_id=sid, data=canonical)

    def recent(self,agent_id,session_id=None):
        with self.app.state.store.session() as db:
            query=select(EventRow).where(EventRow.type=='native.observation',EventRow.agent_id==agent_id)
            if session_id: query=query.where(EventRow.session_id==session_id)
            rows=db.scalars(query.order_by(EventRow.cursor.desc()).limit(100)).all()
            return [dict(row.data) for row in rows]

    def status(self,agent_id):
        state=dict(self.states.get(agent_id,{}))
        client=next((c for c in self.clients() if c.agent_id==agent_id),None)
        if client and client.state=='connected':
            try:
                response=client._request('hooks/list',{'cwds':[]})
                hooks=[h for entry in response['data'] for h in entry['hooks'] if h.get('pluginId')=='astrorder@astrorder-local']
                state['configured_events']=len(hooks)
                state['trusted']=bool(hooks) and all(h.get('trustStatus') in ('trusted','managed') and h.get('enabled') for h in hooks)
                state['needs_review']=any(h.get('trustStatus') in ('untrusted','modified') for h in hooks)
            except (RuntimeError, OSError, ValueError, KeyError):
                state['native_status_available']=False
        return state

    def install(self,agent_id):
        import sys

        from . import observer_plugin
        client=next((c for c in self.clients() if c.agent_id==agent_id and c.state=='connected'),None)
        if not client or not client._home: raise ValueError('Codex must be connected before installing observer')
        script=Path(__file__).resolve().parents[3]/'connectors/codex/observer_hook.py'
        content=script.read_text(encoding='utf-8')
        home=client._home.as_posix()
        if hasattr(client,'remote_json'):
            source=inspect.getsource(observer_plugin)+'\nimport sys\nprint(json.dumps(install_plugin('+repr(home)+',sys.executable,'+repr(content)+','+repr(client._executable())+')))'
            result=client.remote_json(source)
        else:
            result=observer_plugin.install_plugin(home,sys.executable,content,client._executable())
        self.states.setdefault(agent_id,{})['installed']=True
        return result
