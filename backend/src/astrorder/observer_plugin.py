"""Codex plugin packaging and migration; executable on local or SSH Python."""
import hashlib
import json
import os
import queue
import shlex
import subprocess
import threading
import time
from pathlib import Path


def verify_plugin(home, codex):
    env=dict(os.environ);env['CODEX_HOME']=str(home);env['PATH']=str(Path(codex).parent)+os.pathsep+env.get('PATH','')
    process=subprocess.Popen([str(codex),'app-server','--listen','stdio://'],env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,encoding='utf-8')
    lines=queue.Queue()
    def read():
        for line in process.stdout:
            lines.put(json.loads(line))
    thread=threading.Thread(target=read,daemon=True);thread.start()
    def send(data): process.stdin.write(json.dumps(data)+'\n');process.stdin.flush()
    def response(identifier):
        end=time.monotonic()+30
        while time.monotonic()<end:
            row=lines.get(timeout=max(.01,end-time.monotonic()))
            if row.get('id')==identifier:
                if 'error' in row: raise RuntimeError('Native plugin verification rejected')
                return row['result']
        raise RuntimeError('Native verification timeout')
    try:
        send({'id':1,'method':'initialize','params':{'clientInfo':{'name':'astrorder-plugin-check','version':'1'},'capabilities':{'experimentalApi':True}}});response(1)
        send({'method':'initialized','params':{}})
        send({'id':2,'method':'hooks/list','params':{'cwds':[]}})
        hooks=[h for entry in response(2)['data'] for h in entry['hooks'] if h.get('pluginId')=='astrorder@astrorder-local']
        if len(hooks)!=10 or any(h['source']!='plugin' for h in hooks): raise RuntimeError('Plugin hook ownership not confirmed')
        return {'plugin_hooks':len(hooks),'source':'plugin','trusted':bool(hooks) and all(h.get('trustStatus') in ('trusted','managed') and h.get('enabled') for h in hooks),'needs_review':any(h.get('trustStatus') in ('untrusted','modified') for h in hooks)}
    finally:
        process.stdin.close()
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired: process.terminate();process.wait(timeout=5)

EVENTS=('SessionStart','SessionEnd','UserPromptSubmit','PreToolUse','PostToolUse','PermissionRequest','SubagentStart','SubagentStop','Stop','Interrupt')
MARKER='Astrorder passive observation'

def install_plugin(home, python, source, codex):
    home=Path(home)
    root=home/'astrorder-observer/marketplace'
    plugin=root/'plugins/astrorder'
    (plugin/'.codex-plugin').mkdir(parents=True,exist_ok=True)
    (plugin/'hooks').mkdir(exist_ok=True)
    (root/'.agents/plugins').mkdir(parents=True,exist_ok=True)
    script=plugin/'observer.py'; script.write_text(source,encoding='utf-8')
    argv=[str(python),str(script),'--spool',str(home/'astrorder-observer/events')]
    # A bare safe executable works in both PowerShell and Bash. Quoting the
    # executable alone makes PowerShell parse it as a string, not a command.
    if os.name=='nt':
        executable=str(python).replace(chr(92),'/')
        if any(c.isspace() for c in executable):
            import ctypes
            buffer=ctypes.create_unicode_buffer(32768)
            if not ctypes.windll.kernel32.GetShortPathNameW(str(python),buffer,len(buffer)):
                raise ValueError('Cannot resolve a shell-safe Python executable')
            executable=buffer.value.replace(chr(92),'/')
        if not all(c.isalnum() or c in ':/._-' for c in executable):
            raise ValueError('Python executable is not shell-safe')
        command=executable+' '+ ' '.join('"'+arg.replace(chr(92),'/')+'"' for arg in argv[1:])
    else:
        command=shlex.join(argv)
    manifest={'name':'astrorder','version':'0.2.1','description':'星序：原生会话活动观察与通知','interface':{'displayName':'Astrorder · 星序','shortDescription':'会话、工具、子代理活动与完成通知'},'hooks':'./hooks/hooks.json'}
    skill_dir=plugin/'skills/astrorder'
    skill_dir.mkdir(parents=True,exist_ok=True)
    (skill_dir/'SKILL.md').write_text(
        '---\nname: astrorder\ndescription: Use the Astrorder MCP server to read sessions, list agents and machines.\n---\n\n'
        'Use the MCP server named astrorder only. Tools: catalog_list, sessions_list (compact, limit 30), sessions_search, sessions_read, agents_list, machines_list, projects_list. '
        'Session keys look like agent_id::session_id. Call sessions_read with {"key":"agent_id::session_id"}.\n',
        encoding='utf-8',
    )
    manifest['skills']='./skills/'
    port=os.environ.get('ASTRORDER_PORT','30001')
    token=os.environ.get('ASTRORDER_AGENT_TOKEN') or os.environ.get('ASTRORDER_BROWSER_SECRET') or os.environ.get('ASTRORDER_CONNECTOR_SECRET') or ''
    mcp_server={'type':'http','url':f'http://127.0.0.1:{port}/api/v1/agent/mcp','bearer_token_env_var':'ASTRORDER_AGENT_TOKEN'}
    if token:
        mcp_server['env']={'ASTRORDER_AGENT_TOKEN':token}
    (plugin/'.mcp.json').write_text(json.dumps({'mcpServers':{'astrorder':mcp_server}},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    manifest['mcpServers']='./.mcp.json'
    hooks={'description':'Astrorder · 星序观察钩子','hooks':{event:[{'hooks':[{'type':'command','command':command,'timeout':600 if event=='PermissionRequest' else 3,'statusMessage':MARKER}]}] for event in EVENTS}}
    marketplace={'name':'astrorder-local','interface':{'displayName':'星序本地插件'},'plugins':[{'name':'astrorder','source':{'source':'local','path':'./plugins/astrorder'},'policy':{'installation':'AVAILABLE','authentication':'ON_USE'},'category':'Productivity'}]}
    for path,data in [(plugin/'.codex-plugin/plugin.json',manifest),(plugin/'hooks/hooks.json',hooks),(root/'.agents/plugins/marketplace.json',marketplace)]:
        path.write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    env=dict(os.environ); env['CODEX_HOME']=str(home); env['PATH']=str(Path(codex).parent)+os.pathsep+env.get('PATH','')
    config=home/'config.toml'
    if config.exists():
        backup=home/'astrorder-observer'/('config-before-plugin-'+str(time.time_ns())+'.toml')
        backup.write_bytes(config.read_bytes()); backup.chmod(0o600)
    for args in [('plugin','marketplace','add',str(root),'--json'),('plugin','add','astrorder@astrorder-local','--json')]:
        result=subprocess.run([str(codex),*args],env=env,capture_output=True,text=True,encoding='utf-8',timeout=60,check=False)
        if result.returncode: raise RuntimeError('Native plugin installation failed: '+str(result.returncode))
    verified=verify_plugin(home,codex)
    migrated=remove_legacy_hooks(home)
    return {'installed':True,'plugin_id':'astrorder@astrorder-local','events':len(EVENTS),'script_sha256':hashlib.sha256(source.encode()).hexdigest(),**verified,**migrated}

def remove_legacy_hooks(home):
    home=Path(home); config=home/'hooks.json'
    if not config.exists(): return {'removed':0}
    before=config.read_bytes(); data=json.loads(before); removed=0
    for event,groups in list(data.get('hooks',{}).items()):
        kept=[]
        for group in groups:
            handlers=[]
            for handler in group.get('hooks',[]):
                if handler.get('statusMessage')==MARKER and 'astrorder-observer' in handler.get('command',''): removed+=1
                else: handlers.append(handler)
            if handlers: kept.append({**group,'hooks':handlers})
            elif not group.get('hooks'): kept.append(group)
        if kept: data['hooks'][event]=kept
        else: data['hooks'].pop(event,None)
    if removed:
        backup=home/'astrorder-observer'/('hooks-before-plugin-'+str(time.time_ns())+'.json'); backup.write_bytes(before); backup.chmod(0o600)
        temporary=config.with_suffix('.migration.tmp'); temporary.write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n',encoding='utf-8'); os.replace(temporary,config)
        assert json.loads(config.read_text(encoding='utf-8'))==data
    return {'removed':removed}
