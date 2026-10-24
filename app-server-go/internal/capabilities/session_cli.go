package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
)

type SessionCLIStatus struct {
	Ready   bool   `json:"ready"`
	Message string `json:"message"`
	Home    string `json:"home"`
}

type cliDestination struct {
	env        envtarget.Env
	home, kind string
	caller     []byte
}

func (s *Service) cliDestination(ctx context.Context, id string) (*cliDestination, error) {
	if s.Agents == nil {
		return nil, envtarget.Errorf(503, "Agent 配置服务不可用")
	}
	a, err := s.Agents.GetAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, envtarget.Errorf(404, "Agent 不存在")
	}
	value := func(k string) string { v, _ := a[k].(string); return v }
	kind := value("kind")
	if !validAgent(kind) {
		return nil, envtarget.Errorf(422, "此 Agent 暂不支持星序会话 CLI")
	}
	conn := value("connection_id")
	if conn == "" {
		conn = "local"
	}
	profile := value("profile_name")
	if profile == "" {
		profile = "default"
	}
	for _, v := range []string{id, conn, profile, kind} {
		if v == "" || strings.ContainsAny(v, " \t\r\n") {
			return nil, envtarget.Errorf(422, "Agent 身份字段无效")
		}
	}
	env, err := s.open(ctx, conn)
	if err != nil {
		return nil, err
	}
	p, err := env.Paths(ctx)
	if err != nil {
		return nil, err
	}
	home := skillHome(p, kind)
	if home == "" {
		return nil, envtarget.Errorf(422, "无法确定 Agent 配置目录")
	}
	caller, _ := json.Marshal(map[string]string{"agent_id": id, "connection_id": conn, "profile_name": profile, "runtime": kind})
	return &cliDestination{env, home, kind, append(caller, '\n')}, nil
}

func matchingCaller(raw, want []byte) bool {
	var a, b map[string]string
	return json.Unmarshal(raw, &a) == nil && json.Unmarshal(want, &b) == nil && len(a) == 4 && len(b) == 4 && a["agent_id"] == b["agent_id"] && a["connection_id"] == b["connection_id"] && a["profile_name"] == b["profile_name"] && a["runtime"] == b["runtime"]
}

func (s *Service) SessionCLI(ctx context.Context, id string, install bool) (*SessionCLIStatus, error) {
	d, err := s.cliDestination(ctx, id)
	if err != nil {
		return nil, err
	}
	defer s.lock(d.env.Target().ID + "|skills-dispatch|" + d.home + "/skills")()
	callerPath := path.Join(d.home, agentskill.CallerFile)
	if err := checkOfficialTarget(ctx, d.env, d.home, callerPath); err != nil {
		return nil, err
	}
	before, readErr := d.env.Read(ctx, callerPath)
	if readErr != nil && !errors.Is(readErr, envtarget.ErrNotExist) {
		return nil, readErr
	}
	status := &SessionCLIStatus{Home: d.home, Message: "尚未配置星序会话 CLI"}
	if readErr == nil && !matchingCaller(before, d.caller) {
		if install {
			return nil, envtarget.Errorf(409, "目标已有其他 Agent 的身份绑定，已保留原文件")
		}
		status.Message = "现有 CLI 身份绑定与此 Agent 不一致"
		return status, nil
	}
	if !install {
		if readErr != nil {
			return status, nil
		}
		_, _, err := verifiedDestinationCLI(ctx, d.env, d.home, d.kind)
		if err != nil {
			status.Message = err.Error()
			return status, nil
		}
		status.Ready = true
		status.Message = "已配置并核验"
		return status, nil
	}
	provider := s.CLIBinary
	if provider == nil {
		provider = bundledSessionCLI
	}
	bin, binary, err := provider(ctx, d.env)
	if err != nil {
		return nil, err
	}
	if bin != "astrorder-agent" && bin != "astrorder-agent.exe" {
		return nil, envtarget.Errorf(422, "CLI 制品名称无效")
	}
	// Preserve byte-identical bindings because existing modules hash this file.
	caller := d.caller
	if readErr == nil {
		caller = before
	}
	transportPath := path.Join(d.home, "astrorder-transport.json")
	var transport, oldTransport []byte
	var transportReadErr error
	if s.CLITransport != nil {
		transport, err = s.CLITransport(ctx, d.env)
		if err != nil {
			return nil, err
		}
		if err := checkOfficialTarget(ctx, d.env, d.home, transportPath); err != nil {
			return nil, err
		}
		oldTransport, transportReadErr = d.env.Read(ctx, transportPath)
		if transportReadErr != nil && !errors.Is(transportReadErr, envtarget.ErrNotExist) {
			return nil, transportReadErr
		}
	}
	if err := d.env.Write(ctx, callerPath, caller, 0600); err != nil {
		return nil, err
	}
	rollback := func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if transport != nil {
			if transportReadErr == nil {
				_ = d.env.Write(rctx, transportPath, oldTransport, 0600)
			} else {
				_ = d.env.Remove(rctx, transportPath)
			}
		}
		if readErr == nil {
			_ = d.env.Write(rctx, callerPath, before, 0600)
		} else {
			_ = d.env.Remove(rctx, callerPath)
		}
	}
	stored, err := d.env.Read(ctx, callerPath)
	if err != nil || !bytes.Equal(stored, caller) {
		rollback()
		return nil, envtarget.Errorf(502, "身份绑定回读校验失败，已尝试还原")
	}
	skill, _ := officialskills.GetOfficialSkill("astrorder-sessions")
	folder := path.Join(d.home, "skills", skill.Name)
	files := map[string][]byte{"SKILL.md": officialskills.RenderOfficialSkill(*skill, d.home), "scripts/" + bin: binary}
	if err := writeOfficialFiles(ctx, d.env, folder, d.kind, skill.Name, files, func() error {
		if transport != nil {
			if err := d.env.Write(ctx, transportPath, transport, 0600); err != nil {
				return err
			}
			stored, err := d.env.Read(ctx, transportPath)
			if err != nil || !bytes.Equal(stored, transport) {
				return envtarget.Errorf(502, "星序连接配置回读失败")
			}
		}
		_, _, err := readVerifiedCLI(ctx, d.env, d.home, path.Join(folder, "scripts", bin), bin, payloadHash(binary))
		return err
	}); err != nil {
		rollback()
		return nil, err
	}
	status.Ready = true
	status.Message = "已配置并核验"
	return status, nil
}

// Only packaged binaries for the destination platform are installed. Never copy
// an executable or caller binding from another Agent's skill directory.
func bundledSessionCLI(ctx context.Context, env envtarget.Env) (string, []byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	base := filepath.Dir(exe)
	name := "astrorder-agent"
	file := ""
	if env.IsLocal() {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		file = filepath.Join(base, name)
	} else {
		result, err := env.Shell(ctx, "uname -s; uname -m", nil)
		if err != nil {
			return "", nil, err
		}
		parts := strings.Fields(result.Stdout)
		if result.Code != 0 || len(parts) != 2 || parts[0] != "Linux" {
			return "", nil, envtarget.Errorf(422, "目标平台暂不支持星序 CLI 自动配置")
		}
		arch := ""
		switch parts[1] {
		case "x86_64", "amd64":
			arch = "amd64"
		case "aarch64", "arm64":
			arch = "arm64"
		default:
			return "", nil, envtarget.Errorf(422, "目标 CPU 架构暂不支持星序 CLI")
		}
		file = filepath.Join(base, "helpers", "linux-"+arch, name)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", nil, envtarget.Errorf(503, "安装包缺少目标平台星序 CLI 制品：%s", file)
	}
	return name, data, nil
}
