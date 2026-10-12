package memory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

const (
	TargetLocal = "local"
	TargetWSL   = "wsl"
	TargetSSH   = "ssh"

	TargetAppHermes = "hermes"
	TargetAppCodex  = "codex"

	HermesOpenVikingPluginSource = "volcengine/OpenViking/examples/hermes-plugin"
	HermesOpenVikingPluginRef    = "3b65218eef43aba7a680cf143278ad4c153b0e9f"
	HermesOpenVikingPluginName   = "openviking"
	CodexOpenVikingMarketplace   = "https://github.com/volcengine/OpenViking.git"
	CodexOpenVikingPlugin        = "openviking-memory@openviking"
)

// Target keeps model-sync's public machine ID and shape. Home/Profile are adapter-only.
type Target struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Distro        string   `json:"distro,omitempty"`
	ConnectionID  string   `json:"connection_id,omitempty"`
	State         string   `json:"state,omitempty"`
	Agents        []string `json:"agents,omitempty"`
	Home          string   `json:"-"`
	HermesProfile string   `json:"-"`
}

type TargetResolver interface {
	ResolveTargets(context.Context) ([]Target, error)
}

type TargetSelector interface {
	SelectTargets(context.Context, []string) ([]Target, error)
}

type TargetFS interface {
	ReadFile(context.Context, Target, string) ([]byte, error)
	WriteFileAtomic(context.Context, Target, string, []byte, uint32) error
}

type NativeCommand struct {
	Target Target
	Name   string
	Args   []string
	Env    map[string]string
}

type NativeCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type NativeRunner interface {
	Run(context.Context, NativeCommand) (NativeCommandResult, error)
}

type SyncerOptions struct {
	Resolver TargetResolver
	FS       TargetFS
	Runner   NativeRunner
}

type Syncer struct {
	resolver TargetResolver
	fs       TargetFS
	runner   NativeRunner
}

type TargetStatus struct {
	TargetID        string  `json:"target_id"`
	OVCLIStatus     string  `json:"ovcli_status"`
	HooksEnabled    bool    `json:"hooks_enabled"`
	PluginInstalled bool    `json:"plugin_installed"`
	TargetURL       string  `json:"target_url"`
	HermesStatus    string  `json:"hermes_status"`
	HermesEndpoint  string  `json:"hermes_endpoint"`
	RawOutput       *string `json:"raw_output"`
}

type TargetApplyResult struct {
	TargetID string `json:"target_id"`
	Success  bool   `json:"success"`
	Detail   string `json:"detail"`
}

type TargetSyncResult struct {
	Targets []TargetApplyResult `json:"targets"`
	Applied int                 `json:"applied"`
	Failed  int                 `json:"failed"`
}

func NewSyncer(options SyncerOptions) (*Syncer, error) {
	if options.Resolver == nil || options.FS == nil || options.Runner == nil {
		return nil, errors.New("memory target resolver, filesystem, and native runner are required")
	}
	return &Syncer{resolver: options.Resolver, fs: options.FS, runner: options.Runner}, nil
}

func (s *Syncer) InspectSelected(ctx context.Context, config Config, targetIDs []string) ([]TargetStatus, error) {
	targets, err := s.resolveSelected(ctx, targetIDs)
	if err != nil {
		return nil, err
	}
	statuses := make([]TargetStatus, len(targets))
	for index, target := range targets {
		statuses[index] = s.inspectTarget(ctx, target, config)
	}
	return statuses, nil
}

func (s *Syncer) Inspect(ctx context.Context, config Config) ([]TargetStatus, error) {
	return s.InspectSelected(ctx, config, nil)
}

func (s *Syncer) inspectTarget(ctx context.Context, target Target, config Config) TargetStatus {
	status := TargetStatus{TargetID: target.ID, OVCLIStatus: "missing", HermesStatus: "missing"}
	raw, readErr := s.fs.ReadFile(ctx, target, targetOVCLIPath(target))
	if readErr == nil {
		status.OVCLIStatus = "configured"
		var stored Config
		if decodeSingleJSON(raw, &stored) == nil {
			status.TargetURL = stored.URL
		}
	} else if !errors.Is(readErr, ErrTargetFileNotFound) {
		message := sanitizeSecret(readErr.Error(), config.APIKey)
		status.RawOutput = &message
	}
	if hasAgent(target, TargetAppCodex) {
		codexStatus := s.inspectCodexState(ctx, target, config)
		status.HooksEnabled = codexStatus.HooksEnabled
		status.PluginInstalled = codexStatus.PluginInstalled
		if codexStatus.Error != nil {
			message := sanitizeSecret(codexStatus.Error.Error(), config.APIKey)
			status.RawOutput = &message
		}
	}
	if hasAgent(target, TargetAppHermes) {
		hermesStatus := s.inspectHermesState(ctx, target, config)
		status.HermesStatus = hermesStatus.Status
		status.HermesEndpoint = hermesStatus.Endpoint
		if hermesStatus.Error != nil && status.RawOutput == nil {
			message := sanitizeSecret(hermesStatus.Error.Error(), config.APIKey)
			status.RawOutput = &message
		}
	}
	return status
}

func (s *Syncer) ApplySelected(ctx context.Context, config Config, targetIDs []string) (TargetSyncResult, error) {
	return s.applyTargets(ctx, config, targetIDs)
}

func (s *Syncer) Apply(ctx context.Context, config Config) (TargetSyncResult, error) {
	return s.applyTargets(ctx, config, nil)
}

func (s *Syncer) applyTargets(ctx context.Context, config Config, targetIDs []string) (TargetSyncResult, error) {
	if _, err := validateServerURL(config.URL); err != nil {
		return TargetSyncResult{}, err
	}
	targets, err := s.resolveSelected(ctx, targetIDs)
	if err != nil {
		return TargetSyncResult{}, err
	}
	result := TargetSyncResult{Targets: make([]TargetApplyResult, len(targets))}
	var wait sync.WaitGroup
	for index := range targets {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			result.Targets[index] = s.applyTarget(ctx, targets[index], config)
		}()
	}
	wait.Wait()
	for _, target := range result.Targets {
		if target.Success {
			result.Applied++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func (s *Syncer) applyTarget(ctx context.Context, target Target, config Config) TargetApplyResult {
	result := TargetApplyResult{TargetID: target.ID, Success: true, Detail: "OpenViking 配置与 Agent 插件/Hooks 已成功下发并激活"}
	var failures []string
	if err := s.writeOVCLIConfig(ctx, target, config); err != nil {
		failures = append(failures, err.Error())
	}
	if hasAgent(target, TargetAppCodex) {
		if err := s.applyCodex(ctx, target, config); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if hasAgent(target, TargetAppHermes) {
		if err := s.applyHermes(ctx, target, config); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		result.Success = false
		result.Detail = sanitizeSecret(strings.Join(failures, "; "), config.APIKey)
	}
	return result
}

func (s *Syncer) resolveSelected(ctx context.Context, targetIDs []string) ([]Target, error) {
	if len(targetIDs) == 0 {
		if targetIDs != nil {
			return []Target{}, nil
		}
		return s.resolve(ctx)
	}
	selector, ok := s.resolver.(TargetSelector)
	if !ok {
		return nil, errors.New("memory target resolver does not support target selection")
	}
	targets, err := selector.SelectTargets(ctx, targetIDs)
	if err != nil {
		return nil, fmt.Errorf("select memory targets: %w", err)
	}
	return validateTargets(targets)
}

func (s *Syncer) resolve(ctx context.Context) ([]Target, error) {
	targets, err := s.resolver.ResolveTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve memory targets: %w", err)
	}
	return validateTargets(targets)
}

func validateTargets(targets []Target) ([]Target, error) {
	seen := make(map[string]struct{}, len(targets))
	for index := range targets {
		target := &targets[index]
		if target.ID == "" || target.Home == "" {
			return nil, errors.New("memory target ID and home are required")
		}
		if _, duplicate := seen[target.ID]; duplicate {
			return nil, fmt.Errorf("duplicate memory target ID %q", target.ID)
		}
		seen[target.ID] = struct{}{}
		switch target.Kind {
		case TargetLocal, TargetWSL, TargetSSH:
		default:
			return nil, fmt.Errorf("unsupported memory target kind %q", target.Kind)
		}
		if len(target.Agents) == 0 {
			target.Agents = []string{TargetAppCodex, TargetAppHermes}
		}
	}
	return targets, nil
}

func hasAgent(target Target, app string) bool {
	for _, candidate := range target.Agents {
		if candidate == app {
			return true
		}
	}
	return false
}

func targetOVCLIPath(target Target) string {
	return strings.TrimRight(strings.ReplaceAll(target.Home, "\\", "/"), "/") + "/.openviking/ovcli.conf"
}

func validateServerURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("OpenViking URL must be a complete http or https URL without userinfo")
	}
	return parsed, nil
}

func sanitizeSecret(value, secret string) string {
	if secret != "" {
		return strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}
