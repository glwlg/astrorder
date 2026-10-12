package modelconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

var invalidTargetRe = regexp.MustCompile(`[\s\x00-\x1f]`)
var invalidDistroRe = regexp.MustCompile(`[\r\n\x00]`)

func (s *Service) executeRemoteRequest(ctx context.Context, target map[string]any, payload map[string]any) (map[string]any, error) {
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	subCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	helper, err := s.ensureRemoteHelper(subCtx, target)
	if err != nil {
		return nil, err
	}
	cmd, err := s.remoteCommand(subCtx, target, helper)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = bytes.NewReader(reqBytes)
	cmd.Env = process.NativeEnvironment()
	prepareCmdSysProcAttr(cmd)
	stdout := ssh.NewBoundedBuffer(32_000_000)
	stderr := ssh.NewBoundedBuffer(64 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if err := subCtx.Err(); err != nil {
		return nil, err
	}
	if stdout.Truncated() {
		return nil, newProtocolErrorf("remote model helper response exceeds size limit")
	}
	lines := strings.Split(strings.TrimSpace(string(stdout.Bytes())), "\n")
	lastLine := lines[len(lines)-1]
	var resp map[string]any
	decoder := json.NewDecoder(strings.NewReader(lastLine))
	decoder.UseNumber()
	if err := decoder.Decode(&resp); err != nil {
		return nil, newProtocolErrorf("remote model helper returned invalid result")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, newProtocolErrorf("remote model helper returned trailing data")
	}
	ok, _ := resp["ok"].(bool)
	if !ok || runErr != nil {
		detail, _ := resp["detail"].(string)
		if detail == "" {
			detail = "remote model helper execution failed"
		}
		return nil, newProtocolErrorf("%s", detail)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		return nil, newProtocolErrorf("remote model helper result is invalid")
	}
	return result, nil
}

func (s *Service) remoteCommand(ctx context.Context, target map[string]any, executable string, arguments ...string) (*exec.Cmd, error) {
	kind, _ := target["kind"].(string)

	runner := s.config.RemoteCommandRunner

	var cmd *exec.Cmd

	switch kind {
	case "wsl":
		distro, _ := target["distro"].(string)
		if distro == "" || invalidDistroRe.MatchString(distro) {
			return nil, newProtocolErrorf("WSL 发行版无效")
		}
		args := append([]string{"-d", distro, "--exec", executable}, arguments...)
		if runner != nil {
			cmd = runner(ctx, "wsl.exe", args...)
		} else {
			cmd = exec.CommandContext(ctx, "wsl.exe", args...)
		}
	case "ssh":
		settings, ok := target["settings"].(map[string]any)
		if !ok || settings == nil {
			return nil, newProtocolErrorf("SSH 设置无效")
		}
		host, _ := settings["host"].(string)
		alias, _ := settings["ssh_config_alias"].(string)
		effectiveHost := host
		if alias != "" {
			effectiveHost = alias
		}
		if effectiveHost == "" || invalidTargetRe.MatchString(effectiveHost) {
			return nil, newProtocolErrorf("SSH 目标无效")
		}
		port := 0
		if pVal, ok := settings["port"]; ok && pVal != nil {
			switch p := pVal.(type) {
			case json.Number:
				n, err := p.Int64()
				if err != nil {
					return nil, newProtocolErrorf("SSH 端口无效")
				}
				port = int(n)
			case float64:
				port = int(p)
				if float64(port) != p {
					return nil, newProtocolErrorf("SSH 端口无效")
				}
			case int:
				port = p
			default:
				return nil, newProtocolErrorf("SSH 端口无效")
			}
			if port < 1 || port > 65535 {
				return nil, newProtocolErrorf("SSH 端口无效")
			}
		}
		user, _ := settings["user"].(string)
		identity, _ := settings["identity_file"].(string)
		sshExec, _ := settings["ssh_executable"].(string)
		if sshExec == "" {
			sshExec = "ssh"
		}

		sshCfg := ssh.Config{
			Host:           effectiveHost,
			Port:           port,
			User:           user,
			IdentityFile:   identity,
			Binary:         sshExec,
			ConnectTimeout: 12 * time.Second,
			Options:        map[string]string{"BatchMode": "yes"},
		}
		if err := sshCfg.Validate(); err != nil {
			return nil, newProtocolErrorf("SSH 设置无效: %v", err)
		}

		remoteCmd, err := ssh.BuildRemoteCommand(ssh.CommandSpec{
			Executable: executable,
			Args:       arguments,
		})
		if err != nil {
			return nil, fmt.Errorf("build remote command: %w", err)
		}

		baseArgv, err := sshCfg.BuildArgv(remoteCmd)
		if err != nil {
			return nil, newProtocolErrorf("SSH 参数无效: %v", err)
		}

		if runner != nil {
			cmd = runner(ctx, baseArgv[0], baseArgv[1:]...)
		} else {
			cmd = exec.CommandContext(ctx, baseArgv[0], baseArgv[1:]...)
		}
	default:
		return nil, newProtocolErrorf("模型配置目标类型无效")
	}

	if cmd == nil {
		return nil, newProtocolErrorf("remote model helper command is unavailable")
	}
	return cmd, nil
}
