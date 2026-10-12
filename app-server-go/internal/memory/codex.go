package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	codexMinPluginVersion = "0.114.0"
)

var semverPattern = regexp.MustCompile(`(?m)(\d+)\.(\d+)\.(\d+)`)

func (s *Syncer) applyCodex(ctx context.Context, target Target, config Config) error {
	version, err := s.codexVersion(ctx, target, config.APIKey)
	if err != nil {
		return err
	}
	if compareSemver(version, codexMinPluginVersion) < 0 {
		return fmt.Errorf("Codex %s does not support the OpenViking plugin contract (minimum %s)", version, codexMinPluginVersion)
	}
	if err := s.configureCodexHooks(ctx, target, config, version); err != nil {
		return err
	}
	if _, err := s.runChecked(ctx, target, "codex", []string{"plugin", "marketplace", "add", CodexOpenVikingMarketplace}, nil, config.APIKey); err != nil {
		return fmt.Errorf("register Codex OpenViking marketplace: %w", err)
	}
	if _, err := s.runChecked(ctx, target, "codex", []string{"plugin", "add", CodexOpenVikingPlugin}, nil, config.APIKey); err != nil {
		return fmt.Errorf("enable Codex OpenViking plugin: %w", err)
	}
	state := s.inspectCodexState(ctx, target, config)
	if state.Error != nil {
		return state.Error
	}
	if !state.PluginInstalled || !state.HooksEnabled {
		return errors.New("Codex OpenViking plugin or hooks are not enabled")
	}
	return nil
}

func (s *Syncer) writeOVCLIConfig(ctx context.Context, target Target, config Config) error {
	filename := targetOVCLIPath(target)
	previous, readErr := s.fs.ReadFile(ctx, target, filename)
	if readErr != nil && !errors.Is(readErr, ErrTargetFileNotFound) {
		return fmt.Errorf("read Codex OpenViking config: %w", readErr)
	}
	next, err := mergeJSONFields(previous, map[string]string{"url": config.URL, "api_key": config.APIKey})
	if err != nil {
		return fmt.Errorf("merge Codex OpenViking config: %w", err)
	}
	if err := s.fs.WriteFileAtomic(ctx, target, filename, next, 0o600); err != nil {
		return fmt.Errorf("write Codex OpenViking config: %w", err)
	}
	actual, err := s.fs.ReadFile(ctx, target, filename)
	if err != nil {
		return fmt.Errorf("read back Codex OpenViking config: %w", err)
	}
	if err := verifyJSONFields(actual, config); err != nil {
		if readErr == nil {
			if rollbackErr := s.fs.WriteFileAtomic(ctx, target, filename, previous, 0o600); rollbackErr != nil {
				return fmt.Errorf("%w; rollback failed: %s", err, sanitizeSecret(rollbackErr.Error(), config.APIKey))
			}
		}
		return err
	}
	return nil
}

type codexInspection struct {
	HooksEnabled    bool
	PluginInstalled bool
	Error           error
}

func (s *Syncer) configureCodexHooks(ctx context.Context, target Target, config Config, version string) error {
	features, err := s.runChecked(ctx, target, "codex", []string{"features", "list"}, nil, config.APIKey)
	if err != nil {
		return fmt.Errorf("inspect Codex feature flags: %w", err)
	}
	featureKey, err := codexHooksFeature(features.Stdout)
	if err != nil {
		return fmt.Errorf("Codex %s: %w", version, err)
	}
	filename := strings.TrimRight(strings.ReplaceAll(target.Home, "\\", "/"), "/") + "/.codex/config.toml"
	previous, readErr := s.fs.ReadFile(ctx, target, filename)
	if readErr != nil && !errors.Is(readErr, ErrTargetFileNotFound) {
		return fmt.Errorf("read Codex config: %w", readErr)
	}
	next, err := ensureTOMLBoolean(previous, "features", featureKey, true)
	if err != nil {
		return fmt.Errorf("merge Codex config: %w", err)
	}
	if err := s.fs.WriteFileAtomic(ctx, target, filename, next, 0o600); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	actual, err := s.fs.ReadFile(ctx, target, filename)
	if err != nil || !hasTOMLBoolean(actual, "features", featureKey, true) {
		if readErr == nil {
			if rollbackErr := s.fs.WriteFileAtomic(ctx, target, filename, previous, 0o600); rollbackErr != nil {
				return fmt.Errorf("verify Codex config failed; rollback failed: %s", sanitizeSecret(rollbackErr.Error(), config.APIKey))
			}
		}
		return errors.New("effective Codex hook feature is not enabled")
	}
	return nil
}

func codexHooksFeature(output string) (string, error) {
	if featureAdvertised(output, "hooks") {
		return "hooks", nil
	}
	if featureAdvertised(output, "plugin_hooks") {
		return "plugin_hooks", nil
	}
	return "", errors.New("runtime does not advertise hooks or plugin_hooks")
}

func featureAdvertised(output, key string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key && fields[len(fields)-2] != "removed" {
			return true
		}
	}
	return false
}

func (s *Syncer) inspectCodexState(ctx context.Context, target Target, config Config) codexInspection {
	if _, err := s.codexVersion(ctx, target, config.APIKey); err != nil {
		return codexInspection{Error: err}
	}
	features, err := s.runChecked(ctx, target, "codex", []string{"features", "list"}, nil, config.APIKey)
	if err != nil {
		return codexInspection{Error: fmt.Errorf("inspect Codex feature flags: %w", err)}
	}
	featureKey, featureErr := codexHooksFeature(features.Stdout)
	if featureErr != nil {
		return codexInspection{Error: featureErr}
	}
	configFilename := strings.TrimRight(strings.ReplaceAll(target.Home, "\\", "/"), "/") + "/.codex/config.toml"
	configRaw, configErr := s.fs.ReadFile(ctx, target, configFilename)
	if configErr != nil {
		return codexInspection{Error: fmt.Errorf("read Codex config: %w", configErr)}
	}
	result := codexInspection{HooksEnabled: hasTOMLBoolean(configRaw, "features", featureKey, true)}
	plugins, err := s.runChecked(ctx, target, "codex", []string{"plugin", "list", "--json"}, nil, config.APIKey)
	if err != nil {
		result.Error = fmt.Errorf("inspect Codex plugins: %w", err)
		return result
	}
	type installedPlugin struct {
		Name    string `json:"name"`
		ID      string `json:"pluginId"`
		Enabled bool   `json:"enabled"`
	}
	var installed []installedPlugin
	if err := json.Unmarshal([]byte(plugins.Stdout), &installed); err != nil {
		var envelope struct {
			Installed []installedPlugin `json:"installed"`
		}
		if err := json.Unmarshal([]byte(plugins.Stdout), &envelope); err != nil || envelope.Installed == nil {
			result.Error = errors.New("Codex returned invalid plugin status JSON")
			return result
		}
		installed = envelope.Installed
	}
	for _, plugin := range installed {
		if (plugin.Name == CodexOpenVikingPlugin || plugin.ID == CodexOpenVikingPlugin) && plugin.Enabled {
			result.PluginInstalled = true
			break
		}
	}
	return result
}

func (s *Syncer) codexVersion(ctx context.Context, target Target, secret string) (string, error) {
	result, err := s.runChecked(ctx, target, "codex", []string{"--version"}, nil, secret)
	if err != nil {
		return "", fmt.Errorf("inspect Codex version: %w", err)
	}
	matches := semverPattern.FindStringSubmatch(result.Stdout)
	if len(matches) != 4 {
		return "", errors.New("Codex did not report a supported semantic version")
	}
	return strings.Join(matches[1:], "."), nil
}

func compareSemver(left, right string) int {
	leftParts := semverPattern.FindStringSubmatch(left)
	rightParts := semverPattern.FindStringSubmatch(right)
	for index := 1; index <= 3; index++ {
		if leftParts[index] < rightParts[index] {
			if len(leftParts[index]) < len(rightParts[index]) || (len(leftParts[index]) == len(rightParts[index]) && leftParts[index] < rightParts[index]) {
				return -1
			}
		}
		if len(leftParts[index]) > len(rightParts[index]) || (len(leftParts[index]) == len(rightParts[index]) && leftParts[index] > rightParts[index]) {
			return 1
		}
	}
	return 0
}
