package backups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/targetfs"
)

func (s *Service) runRestoreHook(ctx context.Context, plan restorePlan, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if agent := plan.PostRestoreRestartAgent; agent != nil {
		if err := s.validateRestoreAgent(ctx, *agent); err != nil {
			return err
		}
		if err := s.agentRestarter.RestartEnvironmentAgent(ctx, agent.EnvironmentID, agent.Kind); err != nil {
			if ctx.Err() != nil {
				return errors.New("等待 Agent 重启确认超时或已取消。")
			}
			return fmt.Errorf("Agent 重启未完成：%w", err)
		}
		return nil
	}
	var err error
	if plan.ConnectionID != targetfs.LocalConnectionID {
		err = s.resolver.RunBackupRestoreHook(ctx, plan.ConnectionID, dir, plan.PostRestoreCommand)
	} else {
		err = runLocalRestoreHook(ctx, plan, dir)
	}
	if ctx.Err() != nil {
		return errors.New("等待还原后命令结束超时或已取消。")
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("还原后命令退出，退出码 %d。", exit.ExitCode())
	}
	return errors.New("无法执行还原后命令，请检查目标连接、工作目录和命令解释器。")
}

func (s *Service) validateRestoreAgent(ctx context.Context, agent RestoreAgentTarget) error {
	switch agent.Kind {
	case "codex", "hermes", "grok", "antigravity":
	default:
		return policyError(422, "请选择受支持的 Agent。")
	}
	if agent.EnvironmentID == "" || strings.TrimSpace(agent.EnvironmentID) != agent.EnvironmentID {
		return policyError(422, "请选择 Agent 所在环境。")
	}
	if agent.EnvironmentID != targetfs.LocalConnectionID {
		environment, err := s.store.GetSSHConnection(ctx, agent.EnvironmentID)
		if err != nil {
			return err
		}
		if environment == nil {
			return policyError(404, "Agent 所在环境不存在。")
		}
	}
	if s.agentRestarter == nil {
		return policyError(503, "Agent 重启服务不可用。")
	}
	return nil
}

func runLocalRestoreHook(ctx context.Context, plan restorePlan, dir string) error {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !plan.samePath(canonical, dir) {
		return errors.New("还原后命令的工作目录发生变化")
	}
	cmd, err := localRestoreHookCommand(ctx, plan.PostRestoreCommand)
	if err != nil {
		return err
	}
	cmd.Dir = dir
	cmd.Env = restoreHookEnvironment()
	cmd.WaitDelay = 2 * time.Second
	configureChildProcess(cmd)
	return cmd.Run()
}

func restoreHookEnvironment() []string {
	// Keep the user and shell environment needed by ordinary commands, without
	// inheriting the App Server's authentication and service credentials.
	keys := []string{
		"PATH", "PATHEXT", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE",
		"USERPROFILE", "USERNAME", "USERDOMAIN", "HOMEDRIVE", "HOMEPATH",
		"APPDATA", "LOCALAPPDATA", "SystemRoot", "SystemDrive", "WINDIR", "COMSPEC",
		"TEMP", "TMP", "TMPDIR", "ProgramData", "ProgramFiles", "ProgramFiles(x86)",
	}
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, strings.Join([]string{key, value}, "="))
		}
	}
	return env
}
