package remote

// The remote launcher selects its managed Python/dependency generation. The
// relay owns a viewer only; EOF never shuts down the shared gateway owner.
const hermesOwnerRelay = `import asyncio, json, sys
from hermes_constants import get_hermes_home
from hermes_cli.gateway_runtime import ensure_gateway_runtime
from hermes_cli.gateway_client import _session_ticket, gateway_ws_target
from websockets.asyncio.client import connect

async def relay():
    home = get_hermes_home().resolve()
    found = await asyncio.to_thread(ensure_gateway_runtime, home, idle_exit=True)
    if found.state != 'ready' or found.endpoint is None:
        raise RuntimeError('Hermes owner is not ready')
    url, protocols = gateway_ws_target(found.endpoint, _session_ticket(home, found.endpoint))
    async with connect(url, subprotocols=protocols, open_timeout=10, max_size=32*1024*1024, proxy=None) as ws:
        if ws.subprotocol != 'hermes-gateway-v1':
            raise RuntimeError('Hermes owner protocol mismatch')
        reader = asyncio.StreamReader()
        protocol = asyncio.StreamReaderProtocol(reader)
        pipe, _ = await asyncio.get_running_loop().connect_read_pipe(lambda: protocol, sys.stdin.buffer)
        sys.stdout.write(json.dumps({'jsonrpc':'2.0','method':'event','params':{'type':'gateway.ready','payload':{}}})+'\n')
        sys.stdout.flush()
        async def send():
            while raw := await reader.readline():
                await ws.send(raw.decode('utf-8').strip())
        async def receive():
            async for raw in ws:
                if isinstance(raw, bytes): raw = raw.decode('utf-8')
                sys.stdout.write(raw+'\n')
                sys.stdout.flush()
        tasks = [asyncio.create_task(send()), asyncio.create_task(receive())]
        try:
            done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
            for task in done: task.result()
        finally:
            for task in tasks: task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
            pipe.close()
asyncio.run(relay())
`

// argv values stay data through every shell/bootstrap boundary. Never expose
// discovery output: it includes the private viewer ticket.
const hermesRelayBootstrap = `import json, os, subprocess, sys
launcher, profile, relay = sys.argv[1:4]
args = [launcher]
if profile != 'default': args += ['--profile', profile]
args += ['--print-runtime-command', '--module', 'tui_gateway.entry']
command = json.loads(subprocess.check_output(args))
marker = "runpy.run_module('tui_gateway.entry', run_name='__main__', alter_sys=True)"
found = False
for i, arg in enumerate(command):
    if marker in arg:
        command[i] = arg.replace(marker, 'exec('+repr(relay)+')', 1)
        found = True
        break
if not found: raise RuntimeError('Hermes launcher does not expose the managed gateway bootstrap')
os.execv(command[0], command)
`
