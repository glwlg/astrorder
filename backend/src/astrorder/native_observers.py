"""Passive native observations; never create messages, approve tools or claim turn ownership."""
import asyncio
import inspect
import re
import time
from pathlib import Path
from uuid import UUID

from sqlalchemy import select

from .models import EventRow
from .observer_io import read_spool

LABELS={'SessionStart':'原生会话已打开','SessionEnd':'原生会话已关闭','UserPromptSubmit':'原生端提交了消息','PreToolUse':'工具开始执行','PostToolUse':'工具执行结束','PermissionRequest':'原生端等待审批','SubagentStart':'子代理已启动','SubagentStop':'子代理已结束','Stop':'原生轮次已结束','Interrupt':'原生轮次已中断'}

def validate_observation(row):
    if not isinstance(row,dict) or row.get('event') not in LABELS: raise ValueError('Invalid observation')
    if not isinstance(row.get('id'),str) or not re.fullmatch(r'[a-f0-9]{32}(?:[a-f0-9]{32})?',row['id']): raise ValueError('Invalid event ID')
    sid=row.get('session_id'); UUID(sid)
    if not isinstance(row.get('observed_at'),(int,float)) or not time.time()-86400 <= row['observed_at'] <= time.time()+300: raise ValueError('Invalid observation time')
    data={key:row[key] for key in ('id','session_id','event','observed_at')}
    for key in ('turn_id','tool_call_id','tool_name','subagent_id'):
        value=row.get(key)
        if isinstance(value,str) and len(value)<=160 and all(ord(c)>=32 for c in value): data[key]=value
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
        self.stopping=asyncio.Event()

    def clients(self):
        env=self.app.state.environments
        return [env.codex,*env.remote.values()]

    def collect(self, client):
        if not client._home or client.state!='connected': return
        aid=client.agent_id
        home=client._home.as_posix()
        acknowledged=self.ack.get(aid,[])
        if hasattr(client,'remote_json'):
            source='import json,re\nfrom pathlib import Path\n'+inspect.getsource(read_spool)+'\nprint(json.dumps(read_spool('+repr(home)+','+repr(acknowledged)+')))'
            response=client.remote_json(source)
        else:
            response=read_spool(Path(home),acknowledged)
        self.ack[aid]=[]
        state=self.states.setdefault(aid,{})
        state.update({'installed':response['installed'],'poll_ok':True})
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
            event=self.app.state.store.append_event(event_id='observer-'+data['id'],event_type='native.observation',agent_id=aid,session_id=data['session_id'],data=data)
            self.app.state.service._publish(event)
            self.ack[aid].append(data['id'])
            state['last_event_at']=max(state.get('last_event_at',0),data['observed_at'])

    async def run(self):
        while not self.stopping.is_set():
            async def one(client):
                try: await asyncio.to_thread(self.collect,client)
                except (RuntimeError, OSError, ValueError):
                    self.states.setdefault(client.agent_id,{})['poll_ok']=False
            await asyncio.gather(*(one(c) for c in self.clients()))
            await asyncio.to_thread(self.app.state.service.hermes_approvals.poll)
            await asyncio.to_thread(self._reconcile_commands)
            try: await asyncio.wait_for(self.stopping.wait(),timeout=5)
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
