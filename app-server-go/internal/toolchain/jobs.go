package toolchain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/lifecycle"
)

type InstallRequest struct {
	EnvironmentID string   `json:"environment_id"`
	ToolIDs       []string `json:"tool_ids"`
	RecipeID      string   `json:"recipe_id,omitempty"`
	CustomCommand string   `json:"custom_command,omitempty"`
	IncludeDeps   bool     `json:"include_deps"`
	ProxyURL      string   `json:"proxy_url,omitempty"`
	Mirror        string   `json:"mirror,omitempty"`
	SaveNetwork   bool     `json:"save_network,omitempty"`
	Title         string   `json:"title,omitempty"`
}

type InstallJob struct {
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	EnvironmentID string             `json:"environment_id"`
	ToolIDs       []string           `json:"tool_ids"`
	Status        string             `json:"status"` // running | completed | failed | cancelled
	Detail        string             `json:"detail"`
	Log           string             `json:"log"`
	CreatedAt     string             `json:"created_at"`
	Current       int                `json:"current"`
	Total         int                `json:"total"`
	Target        string             `json:"target"`
	Report        *EnvironmentReport `json:"report,omitempty"`

	mu     sync.Mutex
	cancel context.CancelFunc
}

func (j *InstallJob) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Log += string(p)
	return len(p), nil
}

func (j *InstallJob) Snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"id":             j.ID,
		"title":          j.Title,
		"environment_id": j.EnvironmentID,
		"tool_ids":       append([]string(nil), j.ToolIDs...),
		"status":         j.Status,
		"detail":         j.Detail,
		"log":            j.Log,
		"created_at":     j.CreatedAt,
		"current":        j.Current,
		"total":          j.Total,
		"target":         j.Target,
	}
	if j.Report != nil {
		out["report"] = j.Report
	}
	return out
}

func (s *Service) ListInstallJobs() []map[string]any {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	items := make([]map[string]any, 0, len(s.jobs))
	for _, job := range s.jobs {
		items = append(items, job.Snapshot())
	}
	return items
}

func (s *Service) GetInstallJob(id string) (map[string]any, error) {
	s.jobMu.Lock()
	job := s.jobs[id]
	s.jobMu.Unlock()
	if job == nil {
		return nil, ErrJobNotFound
	}
	return job.Snapshot(), nil
}

func (s *Service) CancelInstallJob(id string) (map[string]any, error) {
	s.jobMu.Lock()
	job := s.jobs[id]
	s.jobMu.Unlock()
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.cancel != nil {
		job.cancel()
	}
	return job.Snapshot(), nil
}

func (s *Service) StartInstallJob(ctx context.Context, req InstallRequest) (map[string]any, error) {
	if len(req.ToolIDs) == 0 || len(req.ToolIDs) > 20 {
		return nil, envtarget.Errorf(422, "请选择至少一个要安装的工具或 Agent")
	}
	envID := strings.TrimSpace(req.EnvironmentID)
	if envID == "" {
		envID = "local"
	}
	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return nil, err
	}
	target := env.Target()
	targetOS, _ := s.detectPlatform(ctx, env)

	installedMap := map[string]bool{}
	if req.IncludeDeps {
		if pre, err := s.Inspect(ctx, envID); err == nil {
			for _, item := range pre.Items {
				installedMap[item.ID] = item.Installed
			}
		}
	}

	steps, err := ResolveInstallOrder(req.ToolIDs, req.IncludeDeps, installedMap)
	if err != nil {
		return nil, envtarget.Errorf(422, "%s", err.Error())
	}
	if len(steps) == 0 {
		return nil, envtarget.Errorf(422, "所选工具均已安装，无需重复执行")
	}

	effectiveNet := s.ResolveEffectiveNetwork(ctx, target.ID, req.ProxyURL, req.Mirror)
	extraEnv, err := buildInstallEnv(effectiveNet.ProxyURL, effectiveNet.Mirror)
	if err != nil {
		return nil, envtarget.Errorf(422, "%s", err.Error())
	}
	if req.SaveNetwork {
		_, _ = s.SaveNetworkSettings(ctx, "default", NetworkProfile{
			ProxyURL: strings.TrimSpace(req.ProxyURL),
			Mirror:   strings.TrimSpace(req.Mirror),
		})
	}

	toolIDs := make([]string, len(steps))
	for i, step := range steps {
		toolIDs[i] = step.ID
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		if len(steps) == 1 {
			title = fmt.Sprintf("安装 %s（%s）", steps[0].Name, target.Name)
		} else {
			title = fmt.Sprintf("安装 %d 项工具（%s）", len(steps), target.Name)
		}
	}

	jobCtx, cancel := context.WithCancel(s.jobContext)
	job := &InstallJob{
		ID:            "install-" + rand.Text(),
		Title:         title,
		EnvironmentID: target.ID,
		ToolIDs:       toolIDs,
		Status:        "running",
		Detail:        "准备执行安装…",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Total:         len(steps),
		cancel:        cancel,
	}

	s.jobMu.Lock()
	s.jobs[job.ID] = job
	s.jobMu.Unlock()

	s.jobWG.Add(1)
	go func() {
		defer s.jobWG.Done()
		defer cancel()
		s.runInstallJob(jobCtx, job, env, target, targetOS, steps, req, extraEnv)
	}()

	return job.Snapshot(), nil
}

func (s *Service) runInstallJob(
	ctx context.Context,
	job *InstallJob,
	env envtarget.Env,
	target envtarget.Target,
	targetOS string,
	steps []ToolSpec,
	req InstallRequest,
	extraEnv map[string]string,
) {
	success, failed := 0, 0
	for i, spec := range steps {
		if ctx.Err() != nil {
			break
		}
		job.mu.Lock()
		job.Current = i + 1
		job.Target = spec.Name
		job.Detail = fmt.Sprintf("正在安装：%s（%d/%d）", spec.Name, i+1, len(steps))
		job.mu.Unlock()

		fmt.Fprintf(job, "\n[%d/%d] %s (%s)\n", i+1, len(steps), spec.Name, target.Name)

		recipe, err := selectRecipe(spec.ID, targetOS, req.RecipeID, len(steps) == 1, req.CustomCommand)
		if err != nil {
			failed++
			fmt.Fprintf(job, "[ERROR] %s: %s\n", spec.Name, err.Error())
			continue
		}
		fmt.Fprintf(job, "[EXEC] %s\n", recipe.Command)
		if len(extraEnv) > 0 {
			fmt.Fprintf(job, "[ENV] 已应用自定义网络/镜像环境变量\n")
		}

		code, runErr := executeRecipe(ctx, env, targetOS, recipe, extraEnv, job)
		if ctx.Err() != nil {
			break
		}
		if runErr != nil {
			failed++
			fmt.Fprintf(job, "[ERROR] %s: %s\n", spec.Name, runErr.Error())
			continue
		}
		if code != 0 {
			failed++
			fmt.Fprintf(job, "[FAIL] %s 安装退出码：%d\n", spec.Name, code)
			continue
		}
		success++
		fmt.Fprintf(job, "[DONE] %s 安装命令执行完成\n", spec.Name)

		// If a local agent was just installed and discovered, ensure session-daemon.json includes it.
		if target.Kind == "local" && spec.Kind == ItemKindAgent && s.stateDir != "" {
			if found, ok := s.locateBinary(ctx, env, spec); ok {
				if changed, upsertErr := lifecycle.UpsertLocalRuntime(s.stateDir, s.defaultWorkspace, spec.ID, found); upsertErr == nil && changed {
					fmt.Fprintf(job, "[INFO] 已将 %s (%s) 同步写入小内核配置 session-daemon.json\n", spec.Name, found)
				}
			}
		}
	}

	// Automatic re-inspection after install completes
	var postReport *EnvironmentReport
	if ctx.Err() == nil {
		inspectCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if rep, err := s.Inspect(inspectCtx, target.ID); err == nil {
			postReport = &rep
		}
		cancel()
	}

	job.mu.Lock()
	defer job.mu.Unlock()
	job.Report = postReport
	job.Status = "completed"
	job.Detail = fmt.Sprintf("安装结束：成功 %d，失败 %d", success, failed)
	if ctx.Err() != nil {
		job.Status = "cancelled"
		job.Detail = fmt.Sprintf("安装已取消：成功 %d，失败 %d", success, failed)
	} else if failed > 0 {
		job.Status = "failed"
	}
}

func selectRecipe(toolID, targetOS, recipeID string, allowCustom bool, customCommand string) (InstallRecipe, error) {
	if allowCustom && strings.TrimSpace(customCommand) != "" {
		shell := "sh"
		if strings.ToLower(targetOS) == "windows" {
			shell = "powershell"
		}
		return InstallRecipe{
			ID:      "custom",
			Label:   "自定义命令",
			Mode:    InstallModeBackground,
			Shell:   shell,
			Command: strings.TrimSpace(customCommand),
		}, nil
	}
	recipes := RecipesFor(toolID, targetOS)
	if len(recipes) == 0 {
		return InstallRecipe{}, fmt.Errorf("当前平台（%s）暂无 %s 的自动安装方案，请改用交互终端或选择已安装路径", targetOS, toolID)
	}
	if strings.TrimSpace(recipeID) != "" {
		for _, r := range recipes {
			if r.ID == recipeID {
				return r, nil
			}
		}
	}
	return recipes[0], nil
}

func buildInstallEnv(proxyURL, mirror string) (map[string]string, error) {
	out := map[string]string{}
	if p := strings.TrimSpace(proxyURL); p != "" {
		parsed, err := url.Parse(p)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errors.New("代理地址格式无效，例如 http://127.0.0.1:7890")
		}
		out["HTTP_PROXY"] = p
		out["HTTPS_PROXY"] = p
		out["http_proxy"] = p
		out["https_proxy"] = p
	}
	if m := strings.TrimSpace(mirror); m != "" {
		switch strings.ToLower(m) {
		case "cn", "china":
			out["NPM_CONFIG_REGISTRY"] = "https://registry.npmmirror.com"
			out["UV_DEFAULT_INDEX"] = "https://pypi.tuna.tsinghua.edu.cn/simple"
		default:
			parsed, err := url.Parse(m)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return nil, errors.New("镜像地址格式无效")
			}
			out["NPM_CONFIG_REGISTRY"] = m
			out["UV_DEFAULT_INDEX"] = m
		}
	}
	return out, nil
}

func executeRecipe(ctx context.Context, env envtarget.Env, targetOS string, recipe InstallRecipe, extraEnv map[string]string, output io.Writer) (int, error) {
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	if env.IsLocal() {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" || strings.ToLower(targetOS) == "windows" {
			cmd = exec.CommandContext(runCtx, "powershell", "-NoProfile", "-ExecutionPolicy", "ByPass", "-Command", recipe.Command)
		} else {
			cmd = exec.CommandContext(runCtx, "/bin/sh", "-lc", recipe.Command)
		}
		cmd.Env = os.Environ()
		for k, v := range extraEnv {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdout = output
		cmd.Stderr = output
		cmd.WaitDelay = 3 * time.Second
		err := cmd.Run()
		if runCtx.Err() != nil {
			return -1, runCtx.Err()
		}
		if err == nil {
			return 0, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}

	// Remote WSL / SSH execution via env.Shell
	var prefix strings.Builder
	for k, v := range extraEnv {
		prefix.WriteString(fmt.Sprintf("export %s='%s'\n", k, strings.ReplaceAll(v, "'", "'\\''")))
	}
	script := prefix.String() + recipe.Command
	res, err := env.Shell(runCtx, script, nil)
	if res.Stdout != "" {
		_, _ = io.WriteString(output, res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") {
			_, _ = io.WriteString(output, "\n")
		}
	}
	if res.Stderr != "" {
		_, _ = io.WriteString(output, res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			_, _ = io.WriteString(output, "\n")
		}
	}
	if err != nil {
		return -1, err
	}
	return res.Code, nil
}
