package modelconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// remoteScript is the embedded Python payload executed on remote targets (WSL/SSH).
const remoteScript = `
import hashlib,json,os,sys,tomllib
from pathlib import Path
FILES={"codex_config":".codex/config.toml","codex_catalog":".codex/opencodex-catalog.json","codex_magpie_catalog":".codex/magpie-catalog.json","grok_config":".grok/config.toml"}
def digest(data): return hashlib.sha256(data).hexdigest()
def validate(name,data):
    if name=="hermes_config":
        if b"\0" in data: raise ValueError("invalid hermes config")
        return
    if name in ("codex_catalog", "codex_magpie_catalog"): json.loads(data.decode("utf-8-sig"))
    else: tomllib.loads(data.decode("utf-8-sig"))
def inspect():
    out={"_home":str(Path.home())}
    items=dict(FILES); items["hermes_config"]=".hermes/config.yaml"
    for name,rel in items.items():
        path=Path.home()/rel
        data=path.read_bytes() if path.is_file() else b""
        out[name]={"exists":path.is_file(),"sha256":digest(data),"content":data.decode("utf-8",errors="replace")}
    return out
ENV={"OPENCODEX_API_AUTH_TOKEN":".config/environment.d/astrorder-opencodex.conf","MAGPIE_API_KEY":".config/environment.d/astrorder-magpie.conf"}
def env_line(name,key):
    if any(c in key for c in "\r\n\0"): raise ValueError("invalid API key")
    return (name+'="'+key.replace('\\','\\\\').replace('"','\\"')+'"\n').encode()
def credential_values(payload):
    values={}
    raw=payload.get("api_keys")
    if isinstance(raw,dict):
        for name in ENV:
            value=raw.get(name)
            if isinstance(value,str) and value: values[name]=value
    key=payload.get("api_key")
    if isinstance(key,str) and key and "OPENCODEX_API_AUTH_TOKEN" not in values: values["OPENCODEX_API_AUTH_TOKEN"]=key
    return values
def restore(path,current,existed):
    temp=path.with_name("."+path.name+".astrorder-rollback")
    if existed:
        temp.write_bytes(current);os.replace(temp,path)
    else:path.unlink(missing_ok=True)
def apply(payload):
    prepared=[]
    for name,text in payload["files"].items():
        if name=="hermes_config": rel=".hermes/config.yaml"
        elif name in FILES: rel=FILES[name]
        else: raise ValueError("invalid managed file")
        if not isinstance(text,str): raise ValueError("invalid managed file")
        path=Path.home()/rel
        if name=="hermes_config" and not path.is_file(): continue
        data=text.encode("utf-8");validate(name,data);path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b""
        if current==data: continue
        temp=path.with_name("."+path.name+".astrorder-tmp");temp.write_bytes(data)
        try: validate(name,temp.read_bytes())
        except Exception:
            temp.unlink(missing_ok=True)
            for _,_,earlier,_,_ in prepared: earlier.unlink(missing_ok=True)
            raise
        prepared.append((name,path,temp,current,existed))
    envs=[]
    for cname,ckey in credential_values(payload).items():
        path=Path.home()/ENV[cname];path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b"";data=env_line(cname,ckey)
        if current!=data:
            temp=path.with_name("."+path.name+".tmp");temp.write_bytes(data);os.chmod(temp,0o600);envs.append((path,temp,current,existed))
    replaced=[]
    try:
        for name,path,temp,current,existed in prepared:
            if existed:
                backup=path.with_name(path.name+".astrorder-backup-"+str(__import__('time').time_ns()));backup.write_bytes(current)
            os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
        for path,temp,current,existed in envs:
            os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
    except Exception:
        for path,current,existed in reversed(replaced): restore(path,current,existed)
        for _,_,temp,_,_ in prepared: temp.unlink(missing_ok=True)
        for _,temp,_,_ in envs: temp.unlink(missing_ok=True)
        raise
    for _,path,_,_,_ in prepared:
        backups=sorted(path.parent.glob(path.name+".astrorder-backup-*"),key=lambda p:p.stat().st_mtime,reverse=True)
        for stale in backups[3:]: stale.unlink(missing_ok=True)
    return {"changed":[item[0] for item in prepared],"files":inspect()}
try:
    request=json.loads(sys.stdin.read());result=inspect() if request["action"]=="plan" else apply(request)
    print(json.dumps({"ok":True,"result":result},ensure_ascii=False,separators=(",",":")))
except Exception as exc:
    print(json.dumps({"ok":False,"detail":str(exc)[:500]},ensure_ascii=False,separators=(",",":")));raise SystemExit(2)
`

var invalidTargetRe = regexp.MustCompile(`[\s\x00-\x1f]`)
var invalidDistroRe = regexp.MustCompile(`[\r\n\x00]`)

func sshArgv(settings map[string]any) ([]string, error) {
	alias, _ := settings["ssh_config_alias"].(string)
	host, _ := settings["host"].(string)
	target := alias
	if target == "" {
		target = host
	}
	if target == "" || invalidTargetRe.MatchString(target) {
		return nil, newProtocolErrorf("SSH 目标无效")
	}

	argv := []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=12"}
	if alias == "" {
		portVal := settings["port"]
		port := 22
		switch p := portVal.(type) {
		case float64:
			port = int(p)
		case int:
			port = p
		}
		if port < 1 || port > 65535 {
			return nil, newProtocolErrorf("SSH 端口无效")
		}
		argv = append(argv, "-p", fmt.Sprintf("%d", port))
		if identity, ok := settings["identity_file"].(string); ok && identity != "" {
			argv = append(argv, "-i", identity)
		}
		if user, ok := settings["user"].(string); ok && user != "" {
			target = fmt.Sprintf("%s@%s", user, target)
		}
	}
	argv = append(argv, target)
	return argv, nil
}

func executeRemoteRequest(ctx context.Context, target map[string]any, payload map[string]any) (map[string]any, error) {
	encoded := base64.StdEncoding.EncodeToString([]byte(remoteScript))
	pyCommand := fmt.Sprintf("python3 -c \"import base64;exec(base64.b64decode('%s'))\"", encoded)

	kind, _ := target["kind"].(string)
	var argv []string
	switch kind {
	case "wsl":
		distro, _ := target["distro"].(string)
		if distro == "" || invalidDistroRe.MatchString(distro) {
			return nil, newProtocolErrorf("WSL 发行版无效")
		}
		argv = []string{"wsl.exe", "-d", distro, "--", "sh", "-lc", pyCommand}
	case "ssh":
		settings, ok := target["settings"].(map[string]any)
		if !ok || settings == nil {
			return nil, newProtocolErrorf("SSH 设置无效")
		}
		baseArgv, err := sshArgv(settings)
		if err != nil {
			return nil, err
		}
		argv = append(baseArgv, pyCommand)
	default:
		return nil, newProtocolErrorf("模型配置目标类型无效")
	}

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	subCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(subCtx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(reqBytes)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	outStr := strings.TrimSpace(stdout.String())
	lines := strings.Split(outStr, "\n")
	var lastLine string
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastLine = trimmed
			break
		}
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(lastLine), &resp); err != nil {
		if runErr != nil {
			return nil, newProtocolErrorf("目标未返回有效的模型配置结果: %v (%s)", runErr, stderr.String())
		}
		return nil, newProtocolErrorf("目标未返回有效的模型配置结果")
	}

	ok, _ := resp["ok"].(bool)
	if !ok || (runErr != nil && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0) {
		detail, _ := resp["detail"].(string)
		if detail == "" {
			detail = "模型配置目标执行失败"
		}
		return nil, newProtocolErrorf("%s", detail)
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		return nil, newProtocolErrorf("模型配置结果格式错误")
	}
	return result, nil
}
