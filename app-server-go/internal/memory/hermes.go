package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (s *Syncer) applyHermes(ctx context.Context, target Target, config Config) error {
	profileArgs := hermesProfileArgs(target)
	installArgs := append(profileArgs, "plugins", "install", HermesOpenVikingPluginSource, "--ref", HermesOpenVikingPluginRef, "--no-enable")
	if _, err := s.runChecked(ctx, target, "hermes", installArgs, nil, config.APIKey); err != nil {
		return fmt.Errorf("install Hermes OpenViking plugin: %w", err)
	}
	if _, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "plugins", "enable", HermesOpenVikingPluginName), nil, config.APIKey); err != nil {
		return fmt.Errorf("enable Hermes OpenViking plugin: %w", err)
	}
	if _, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "set", "memory.provider", HermesOpenVikingPluginName), nil, config.APIKey); err != nil {
		return fmt.Errorf("select Hermes OpenViking provider: %w", err)
	}
	if _, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "set", "OPENVIKING_ENDPOINT", config.URL), nil, config.APIKey); err != nil {
		return fmt.Errorf("set Hermes OpenViking endpoint: %w", err)
	}
	if config.APIKey == "" {
		if _, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "unset", "OPENVIKING_API_KEY"), nil, config.APIKey); err != nil {
			return fmt.Errorf("clear Hermes OpenViking API key: %w", err)
		}
	} else if _, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "set", "OPENVIKING_API_KEY", config.APIKey), nil, config.APIKey); err != nil {
		return fmt.Errorf("set Hermes OpenViking API key: %w", err)
	}
	return s.inspectHermes(ctx, target, config)
}

type hermesInspection struct {
	Status   string
	Endpoint string
	Error    error
}

func (s *Syncer) inspectHermesState(ctx context.Context, target Target, config Config) hermesInspection {
	profileArgs := hermesProfileArgs(target)
	plugins, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "plugins", "list", "--json"), nil, config.APIKey)
	if err != nil {
		return hermesInspection{Status: "missing", Error: fmt.Errorf("inspect Hermes plugins: %w", err)}
	}
	var installed []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(plugins.Stdout), &installed); err != nil {
		return hermesInspection{Status: "missing", Error: errors.New("Hermes returned invalid plugin status JSON")}
	}
	enabled := false
	for _, plugin := range installed {
		if plugin.Name == HermesOpenVikingPluginName && plugin.Status == "enabled" {
			enabled = true
			break
		}
	}
	if !enabled {
		return hermesInspection{Status: "disabled"}
	}
	providerResult, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "get", "memory.provider", "--json"), nil, config.APIKey)
	if err != nil || strings.TrimSpace(providerResult.Stdout) != strconv.Quote(HermesOpenVikingPluginName) {
		return hermesInspection{Status: "disabled", Error: err}
	}
	urlResult, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "get", "OPENVIKING_ENDPOINT", "--json", "--raw"), nil, config.APIKey)
	if err != nil {
		return hermesInspection{Status: "disabled", Error: fmt.Errorf("inspect Hermes OpenViking URL: %w", err)}
	}
	endpoint, err := strconv.Unquote(strings.TrimSpace(urlResult.Stdout))
	if err != nil {
		return hermesInspection{Status: "disabled", Error: errors.New("Hermes returned an invalid OpenViking URL")}
	}
	if config.APIKey == "" {
		return hermesInspection{Status: "configured", Endpoint: endpoint}
	}
	keyResult, err := s.runChecked(ctx, target, "hermes", append(profileArgs, "config", "get", "OPENVIKING_API_KEY", "--json", "--raw"), nil, config.APIKey)
	if err != nil {
		return hermesInspection{Status: "disabled", Endpoint: endpoint, Error: fmt.Errorf("inspect Hermes OpenViking API key: %w", err)}
	}
	if strings.TrimSpace(keyResult.Stdout) != strconv.Quote(config.APIKey) || endpoint != config.URL {
		return hermesInspection{Status: "disabled", Endpoint: endpoint}
	}
	return hermesInspection{Status: "configured", Endpoint: endpoint}
}

func (s *Syncer) inspectHermes(ctx context.Context, target Target, config Config) error {
	state := s.inspectHermesState(ctx, target, config)
	if state.Error != nil {
		return state.Error
	}
	if state.Status != "configured" {
		return errors.New("Hermes OpenViking plugin is not configured")
	}
	return nil
}

func hermesProfileArgs(target Target) []string {
	if target.HermesProfile == "" || target.HermesProfile == "default" {
		return nil
	}
	return []string{"--profile", target.HermesProfile}
}

func (s *Syncer) runChecked(ctx context.Context, target Target, name string, args []string, env map[string]string, secret string) (NativeCommandResult, error) {
	result, err := s.runner.Run(ctx, NativeCommand{Target: target, Name: name, Args: args, Env: env})
	if err != nil {
		return NativeCommandResult{}, errors.New(sanitizeSecret(err.Error(), secret))
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return result, errors.New(sanitizeSecret(message, secret))
	}
	return result, nil
}
