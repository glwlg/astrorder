"""Hermes approvals remain native, request-scoped and explicitly user-decided."""
import asyncio
import threading
from astrorder.connections import ConnectionError
from astrorder.native.controls import result

class HermesApprovals:
    def __init__(self,connections,service):
        self.connections=connections;self.service=service;self.pending={};self.supported=set();self.lock=threading.RLock()

    def runtimes(self):
        local=self.connections.local
        if local._agent_id and local._state=='connected':yield local._agent_id,local._rpc
        for runtime in list(self.connections._ssh_runtimes.values()):
            if runtime.agent_id:yield runtime.agent_id,runtime.rpc

    def poll(self):
        for aid,rpc in self.runtimes():
            try:
                active=result(rpc,'session.active_list',{})
                for row in active.get('sessions',[]):
                    sid,handle=row.get('session_key'),row.get('id')
                    if not sid or not handle or self.service.store.get_session(aid,sid) is None:continue
                    approvals=result(rpc,'approval.pending',{'session_id':handle}).get('approvals')
                    if not isinstance(approvals,list):continue
                    if aid not in self.supported:
                        self.supported.add(aid);self.service._publish_capabilities(aid)
                    current=set()
                    with self.lock:
                        for entry in approvals:
                            rid=entry.get('request_id')
                            if not isinstance(rid,str) or not rid:continue
                            key=(aid,sid,rid);current.add(key)
                            if key in self.pending:continue
                            data={'id':f'hermes-approval:{aid}:{sid}:{rid}','agent_id':aid,'session_id':sid,'target_id':rid,'title':str(entry.get('description') or 'Hermes 操作等待授权')[:160],'detail':str(entry.get('command') or '')[:4000],'state':'pending','data':{'source':'native-hermes'}}
                            self.pending[key]=(rpc,handle,data)
                            self.service._server_event('approval.upsert',agent_id=aid,session_id=sid,data=data)
                        for key in [k for k in self.pending if k[:2]==(aid,sid) and k not in current]:
                            _,_,data=self.pending.pop(key)
                            self.service._server_event('approval.upsert',agent_id=aid,session_id=sid,data={**data,'state':'resolved'})
            except (ConnectionError,OSError,ValueError):continue

    async def submit(self,command):
        return await asyncio.to_thread(self.respond,command)

    def matches(self,command):
        with self.lock:return (command['agent_id'],command['session_id'],command.get('target_id')) in self.pending

    def respond(self,command):
        aid,sid,rid=command['agent_id'],command['session_id'],command.get('target_id')
        with self.lock:
            record=self.pending.get((aid,sid,rid))
            if not record:return 'failed','审批已过期或不属于当前会话。'
            rpc,handle,data=record
            try:
                current=result(rpc,'approval.pending',{'session_id':handle}).get('approvals',[])
                if not any(r.get('request_id')==rid for r in current):return 'failed','原生审批已被处理；未发送决定。'
                resolved=result(rpc,'approval.respond',{'session_id':handle,'request_id':rid,'choice':'once' if command['action']=='approve' else 'deny','all':False})
                if resolved.get('resolved')!=1:return 'unknown','原生端未确认精确处理此审批。'
                remaining=result(rpc,'approval.pending',{'session_id':handle}).get('approvals',[])
                if any(r.get('request_id')==rid for r in remaining):return 'unknown','原生审批仍在等待，结果未确认。'
            except (ConnectionError,OSError,ValueError):return 'unknown','原生审批结果未确认，不会自动重发。'
            self.pending.pop((aid,sid,rid),None)
            self.service._server_event('approval.upsert',agent_id=aid,session_id=sid,data={**data,'state':'approved' if command['action']=='approve' else 'rejected'})
            updated=self.service.store.set_command_state(aid,sid,command['id'],'completed',None)
            self.service._server_event('command.upsert',agent_id=aid,session_id=sid,data=updated)
            return 'accepted',None

    def snapshot(self):
        with self.lock:return [dict(record[2]) for record in self.pending.values()]
