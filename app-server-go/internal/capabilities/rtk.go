package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

type RTKAgent struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Supported bool   `json:"supported"`
	Detail    string `json:"detail,omitempty"`
}

type RTKDaily struct {
	Date         string `json:"date"`
	Commands     int64  `json:"commands"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	SavedTokens  int64  `json:"saved_tokens"`
}

type RTKSummary struct {
	TotalCommands int64   `json:"total_commands"`
	TotalInput    int64   `json:"total_input"`
	TotalOutput   int64   `json:"total_output"`
	TotalSaved    int64   `json:"total_saved"`
	AvgSavingsPct float64 `json:"avg_savings_pct"`
}

type RTKStatus struct {
	Installed  bool       `json:"installed"`
	Version    *string    `json:"version"`
	Executable *string    `json:"executable"`
	Detail     string     `json:"detail"`
	Agents     []RTKAgent `json:"agents"`
	Daily      []RTKDaily `json:"daily"`
	Summary    RTKSummary `json:"summary"`
}

var versionRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._+-]*`)

const (
	hermesRTKPlugin = "rtk-rewrite"
	rtkInstallURL   = "https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh"
)

// rtkAgentArgs are the official `rtk init` arguments for an agent.
func rtkAgentArgs(kind string, enable bool) ([]string, error) {
	var args []string
	switch kind {
	case "codex":
		args = []string{"init", "-g", "--codex"}
	case "hermes":
		args = []string{"init", "-g", "--agent", "hermes"}
	default:
		return nil, envtarget.Errorf(422, "Agent [%s] 暂不支持 RTK 热切换", kind)
	}
	if !enable {
		args = append(args, "--uninstall")
	}
	return args, nil
}

// rtkFiles are every file `rtk init` may create, change or remove.
func rtkFiles(kind string, p envtarget.Paths) []string {
	if kind == "codex" {
		return []string{p.Codex + "/hooks.json", p.Codex + "/RTK.md", p.Codex + "/AGENTS.md"}
	}
	return []string{p.Hermes + "/config.yaml", p.Hermes + "/plugins/" + hermesRTKPlugin + "/__init__.py", p.Hermes + "/plugins/" + hermesRTKPlugin + "/plugin.yaml"}
}

// rtkAgentState reads the agent's own files rather than trusting CLI output.
func rtkAgentState(kind string, p envtarget.Paths, files map[string][]byte) RTKAgent {
	switch kind {
	case "codex":
		hook := strings.Contains(string(files[p.Codex+"/hooks.json"]), "rtk")
		doc := files[p.Codex+"/RTK.md"] != nil
		ref := strings.Contains(string(files[p.Codex+"/AGENTS.md"]), "RTK.md")
		agent := RTKAgent{Kind: kind, Name: "OpenAI Codex", Supported: true, Enabled: hook && doc && ref, Detail: "未启用"}
		if agent.Enabled {
			agent.Detail = "配置目录: " + p.Codex
		} else if hook || doc || ref {
			agent.Detail = fmt.Sprintf("未启用（半启用：钩子=%t RTK.md=%t AGENTS.md 引用=%t）", hook, doc, ref)
		}
		return agent
	case "hermes":
		dir := p.Hermes + "/plugins/" + hermesRTKPlugin
		filesOK := files[dir+"/__init__.py"] != nil && files[dir+"/plugin.yaml"] != nil
		listed := false
		cfg, err := parseYAMLMap(files[p.Hermes+"/config.yaml"])
		if err != nil {
			return RTKAgent{Kind: kind, Name: "Hermes Agent", Supported: true, Detail: "Hermes 配置无法解析：" + err.Error()}
		}
		listed = contains(asStrings(asMap(cfg["plugins"])["enabled"]), hermesRTKPlugin)
		agent := RTKAgent{Kind: kind, Name: "Hermes Agent", Supported: true, Enabled: filesOK && listed, Detail: "未启用"}
		if agent.Enabled {
			agent.Detail = "插件目录: " + dir
		}
		return agent
	}
	return RTKAgent{Kind: "grok", Name: "xAI Grok", Detail: "RTK 暂不支持 Grok"}
}

func (s *Service) rtkAgents(ctx context.Context, env envtarget.Env, p envtarget.Paths) ([]RTKAgent, error) {
	var names []string
	for _, kind := range []string{"codex", "hermes"} {
		names = append(names, rtkFiles(kind, p)...)
	}
	files, err := env.ReadMany(ctx, names)
	if err != nil {
		return nil, err
	}
	return []RTKAgent{rtkAgentState("codex", p, files), rtkAgentState("hermes", p, files), rtkAgentState("grok", p, files)}, nil
}

func (s *Service) rtkVersion(ctx context.Context, env envtarget.Env, exe string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := env.Run(ctx, envtarget.Command{Name: exe, Args: []string{"--version"}})
	if err != nil || result.Code != 0 {
		return ""
	}
	return versionRE.FindString(result.Stdout + " " + result.Stderr)
}

// RTK reports the install, per-agent state and token savings of one machine.
func (s *Service) RTK(ctx context.Context, targetID, days string) (*RTKStatus, error) {
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return s.rtkStatus(ctx, env, days, time.Now())
}

func (s *Service) rtkStatus(ctx context.Context, env envtarget.Env, days string, now time.Time) (*RTKStatus, error) {
	paths, err := env.Paths(ctx)
	if err != nil {
		return nil, err
	}
	agentsState, err := s.rtkAgents(ctx, env, paths)
	if err != nil {
		return nil, err
	}
	status := &RTKStatus{Agents: agentsState, Daily: []RTKDaily{}}
	name := env.Target().Name
	exe, found := env.Which(ctx, "rtk")
	if !found {
		status.Detail = name + " 未检测到 RTK 安装"
		return status, nil
	}
	status.Installed, status.Executable = true, &exe
	if version := s.rtkVersion(ctx, env, exe); version != "" {
		status.Version = &version
	}
	status.Detail = name + " RTK 环境正常"
	gainCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, runErr := env.Run(gainCtx, envtarget.Command{Name: exe, Args: []string{"gain", "-d", "-f", "json"}})
	var raw struct {
		Daily []gainDay `json:"daily"`
	}
	if runErr == nil && result.Code == 0 && json.Unmarshal([]byte(result.Stdout), &raw) == nil {
		status.Daily, status.Summary = aggregateGain(raw.Daily, days, now)
	} else if runErr == nil && result.Code != 0 {
		status.Detail += "（rtk gain 无法读取统计）"
	}
	return status, nil
}

type gainDay = struct {
	Date         string   `json:"date"`
	Commands     float64  `json:"commands"`
	InputTokens  float64  `json:"input_tokens"`
	OutputTokens float64  `json:"output_tokens"`
	SavedTokens  *float64 `json:"saved_tokens"`
}

// aggregateGain keeps the last `days` days and recomputes the totals from the
// kept rows so the summary always matches the table.
func aggregateGain(items []gainDay, days string, now time.Time) ([]RTKDaily, RTKSummary) {
	var cutoff string
	if days != "all" {
		span, err := strconv.Atoi(days)
		if err != nil || span < 1 {
			span = 30
		}
		cutoff = now.AddDate(0, 0, -(span - 1)).Format("2006-01-02")
	}
	daily := []RTKDaily{}
	var summary RTKSummary
	for _, item := range items {
		if len(item.Date) < 10 {
			continue
		}
		date := item.Date[:10]
		if _, err := time.Parse("2006-01-02", date); err != nil || (cutoff != "" && date < cutoff) {
			continue
		}
		row := RTKDaily{Date: item.Date, Commands: int64(item.Commands), InputTokens: int64(item.InputTokens), OutputTokens: int64(item.OutputTokens)}
		row.SavedTokens = row.InputTokens - row.OutputTokens
		if item.SavedTokens != nil {
			row.SavedTokens = int64(*item.SavedTokens)
		}
		daily = append(daily, row)
		summary.TotalCommands += row.Commands
		summary.TotalInput += row.InputTokens
		summary.TotalOutput += row.OutputTokens
		summary.TotalSaved += row.SavedTokens
	}
	if summary.TotalInput > 0 {
		summary.AvgSavingsPct = float64(int64(float64(summary.TotalSaved)/float64(summary.TotalInput)*10000+0.5)) / 100
	}
	return daily, summary
}

// SetRTKAgent turns RTK on or off for one agent. The agent's files are backed
// up first, the official `rtk init` runs, the result is read back from the
// files, and any mismatch restores the backup.
func (s *Service) SetRTKAgent(ctx context.Context, targetID, kind string, enable bool, days string) (*RTKStatus, error) {
	args, err := rtkAgentArgs(kind, enable)
	if err != nil {
		return nil, err
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	exe, found := env.Which(ctx, "rtk")
	if !found {
		return nil, envtarget.Errorf(400, "%s 未安装 RTK，无法切换 Agent 状态", env.Target().Name)
	}
	paths, err := env.Paths(ctx)
	if err != nil {
		return nil, err
	}
	defer s.lock(env.Target().ID + "|rtk")()
	files := rtkFiles(kind, paths)
	backup, err := env.ReadMany(ctx, files)
	if err != nil {
		return nil, err
	}
	if kind == "hermes" {
		if _, err := parseYAMLMap(backup[paths.Hermes+"/config.yaml"]); err != nil {
			return nil, envtarget.Errorf(422, "Hermes 配置无法解析，未执行 RTK 指令：%s", err)
		}
	}
	restore := func() {
		for _, file := range files {
			if data, had := backup[file]; had {
				_ = env.Write(ctx, file, data, 0o644)
			} else {
				_ = env.Remove(ctx, file)
			}
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, runErr := env.Run(runCtx, envtarget.Command{Name: exe, Args: args, Env: rtkEnv(kind, paths)})
	if runErr != nil {
		restore()
		return nil, runErr
	}
	if result.Code != 0 {
		restore()
		return nil, envtarget.Errorf(500, "执行 RTK 指令失败：%s", tail(cleanOutput(result.Stderr, result.Stdout), 500))
	}
	after, err := s.rtkAgents(ctx, env, paths)
	if err == nil {
		for _, agent := range after {
			if agent.Kind == kind && agent.Enabled != enable {
				err = envtarget.Errorf(500, "RTK 指令执行后状态回读不符（期望 %t，实际 %t），已还原配置备份", enable, agent.Enabled)
			}
		}
	}
	if err != nil {
		restore()
		return nil, err
	}
	return s.rtkStatus(ctx, env, days, time.Now())
}

func rtkEnv(kind string, p envtarget.Paths) map[string]string {
	env := map[string]string{"NO_COLOR": "1", "CI": "1"}
	if kind == "codex" {
		env["CODEX_HOME"] = p.Codex
	} else {
		env["HERMES_HOME"] = p.Hermes
	}
	return env
}

// InstallRTK installs the rtk binary and checks that it runs.
func (s *Service) InstallRTK(ctx context.Context, targetID, days string) (*RTKStatus, error) {
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	defer s.lock(env.Target().ID + "|rtk")()
	if _, found := env.Which(ctx, "rtk"); !found {
		ctx2, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		if err := s.installRTKBinary(ctx2, env); err != nil {
			return nil, err
		}
	}
	exe, found := env.Which(ctx, "rtk")
	if !found || s.rtkVersion(ctx, env, exe) == "" {
		return nil, envtarget.Errorf(500, "%s 上的 RTK 安装未能确认：找不到可运行的 rtk", env.Target().Name)
	}
	return s.rtkStatus(ctx, env, days, time.Now())
}

const installScript = `set -e
dir="$HOME/.local/bin"; mkdir -p "$dir"
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT
unset RTK_SKIP_CHECKSUM
curl -fsSL "$1" -o "$tmp"
RTK_INSTALL_DIR="$dir" sh "$tmp"`

func (s *Service) installRTKBinary(ctx context.Context, env envtarget.Env) error {
	if env.IsLocal() {
		if runtime.GOOS != "windows" {
			result, err := env.Run(ctx, envtarget.Command{Name: "sh", Args: []string{"-c", installScript, "sh", rtkInstallURL}})
			if err != nil {
				return err
			}
			if result.Code != 0 {
				return envtarget.Errorf(500, "RTK 安装脚本失败：%s", tail(cleanOutput(result.Stderr, result.Stdout), 500))
			}
			return nil
		}
		if _, ok := env.Which(ctx, "winget"); !ok {
			return envtarget.Errorf(501, "本机没有 winget，无法自动安装 RTK；请手动安装后刷新")
		}
		result, err := env.Run(ctx, envtarget.Command{Name: "winget", Args: []string{"install", "--id", "rtk-ai.rtk", "-e", "--accept-source-agreements", "--accept-package-agreements"}})
		if err != nil {
			return err
		}
		if result.Code != 0 {
			return envtarget.Errorf(500, "winget 安装 RTK 失败：%s", tail(cleanOutput(result.Stdout, result.Stderr), 500))
		}
		return nil
	}
	result, err := env.Shell(ctx, installScript, nil, rtkInstallURL)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return envtarget.Errorf(500, "远端安装 RTK 失败：%s", tail(cleanOutput(result.Stderr, result.Stdout), 500))
	}
	return nil
}
