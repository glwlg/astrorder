from types import SimpleNamespace
from unittest.mock import Mock
from astrorder.hermes_approvals import HermesApprovals

def setup():
    pending=[{'request_id':'request-1','description':'执行命令','command':'example'}]
    calls=[]
    def rpc(method,params):
        calls.append((method,params))
        if method=='session.active_list': return {'result':{'sessions':[{'session_key':'native-id','id':'handle'}]}}
        if method=='approval.pending':return {'result':{'approvals':list(pending)}}
        if method=='approval.respond':pending.clear();return {'result':{'resolved':1}}
        raise AssertionError(method)
    service=Mock();service.store.get_session.return_value={'id':'native-id'}
    manager=HermesApprovals(SimpleNamespace(local=SimpleNamespace(_agent_id='hermes',_state='connected',_rpc=rpc),_ssh_runtimes={}),service)
    return manager,service,pending,calls

def test_poll_only_observes_and_exact_user_approval_confirms_removal():
    manager,service,pending,calls=setup()
    manager.poll();manager.poll()
    assert len(manager.snapshot())==1
    assert not any(m=='approval.respond' for m,p in calls)
    result=manager.respond({'id':'cmd','agent_id':'hermes','session_id':'native-id','target_id':'request-1','action':'approve'})
    assert result==('accepted',None)
    assert next(p for m,p in calls if m=='approval.respond')=={'session_id':'handle','request_id':'request-1','choice':'once','all':False}
    assert manager.snapshot()==[]
    service.store.set_command_state.assert_called_once_with('hermes','native-id','cmd','completed',None)

def test_cross_session_and_stale_approvals_are_never_sent():
    manager,service,pending,calls=setup();manager.poll()
    command={'id':'cmd','agent_id':'hermes','session_id':'other','target_id':'request-1','action':'approve'}
    assert manager.respond(command)[0]=='failed'
    pending.clear();command['session_id']='native-id'
    assert manager.respond(command)[0]=='failed'
    assert not any(m=='approval.respond' for m,p in calls)
