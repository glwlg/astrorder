package capabilities

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

// Opener lists the machines Astrorder manages and opens one of them.
type Opener interface {
	List(context.Context) ([]envtarget.Target, error)
	Open(context.Context, string) (envtarget.Env, error)
}

// PluginSkill describes a SKILL.md entry contributed by an enabled Astrorder extension/plugin.
type PluginSkill struct {
	PluginID    string
	Name        string
	DisplayName string
	Description string
	Content     string
}

// PluginSkillProvider supplies skills exposed by currently enabled plugins.
type PluginSkillProvider interface {
	ListPluginSkills(ctx context.Context) []PluginSkill
}

// PluginSkillProviderFunc adapts a plain function to PluginSkillProvider.
type PluginSkillProviderFunc func(ctx context.Context) []PluginSkill

func (f PluginSkillProviderFunc) ListPluginSkills(ctx context.Context) []PluginSkill {
	if f == nil {
		return nil
	}
	return f(ctx)
}

type Service struct {
	Targets Opener
	HTTP    *http.Client
	Agents  interface {
		GetAgent(context.Context, string) (map[string]any, error)
	}
	CLIBinary    func(context.Context, envtarget.Env) (string, []byte, error)
	CLITransport func(context.Context, envtarget.Env) ([]byte, error)
	PluginSkills PluginSkillProvider
	Logger       *slog.Logger

	pluginSkillsMu  sync.RWMutex
	autoSyncMu      sync.Mutex
	autoSyncRunning bool
	autoSyncPending bool
	locks           sync.Map
	dispatchJobsMu  sync.RWMutex
	dispatchJobs    []*DispatchJob
}

// SetPluginSkillProvider configures the optional provider for enabled extension SKILL.md entries.
func (s *Service) SetPluginSkillProvider(provider PluginSkillProvider) {
	if s == nil {
		return
	}
	s.pluginSkillsMu.Lock()
	s.PluginSkills = provider
	s.pluginSkillsMu.Unlock()
}

func (s *Service) pluginSkills(ctx context.Context) []PluginSkill {
	if s == nil {
		return nil
	}
	s.pluginSkillsMu.RLock()
	provider := s.PluginSkills
	s.pluginSkillsMu.RUnlock()
	if provider == nil {
		return nil
	}
	return provider.ListPluginSkills(ctx)
}

// SetPluginSkillProvider exposes plugin skill wiring on the HTTP handler wrapper as well.
func (h *handler) SetPluginSkillProvider(provider PluginSkillProvider) {
	if h == nil || h.svc == nil {
		return
	}
	h.svc.SetPluginSkillProvider(provider)
}

// TriggerAutoSync exposes non-blocking skill/CLI sync on the HTTP handler wrapper.
func (h *handler) TriggerAutoSync(ctx context.Context) {
	if h == nil || h.svc == nil {
		return
	}
	h.svc.TriggerAutoSync(ctx)
}

func New(targets Opener) *Service {
	return &Service{Targets: targets, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// lock serialises writes to one machine's agent files.
func (s *Service) lock(key string) func() {
	value, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func (s *Service) open(ctx context.Context, id string) (envtarget.Env, error) {
	return s.Targets.Open(ctx, id)
}

// commit edits one config file. mutate receives the current bytes and returns
// the new ones; verify checks what was actually stored. A failed check puts
// the previous content back.
func (s *Service) commit(ctx context.Context, env envtarget.Env, file string,
	mutate func(orig []byte) ([]byte, error), verify func(stored []byte) error) error {
	orig, err := env.Read(ctx, file)
	existed := true
	if errors.Is(err, envtarget.ErrNotExist) {
		existed, orig, err = false, nil, nil
	}
	if err != nil {
		return err
	}
	updated, err := mutate(orig)
	if err != nil {
		return envtarget.Errorf(422, "%v", err)
	}
	if bytes.Equal(updated, orig) {
		return nil
	}
	if existed {
		_ = env.Write(ctx, file+".astrorder.bak", orig, 0o600)
	}
	if err := env.Write(ctx, file, updated, 0o600); err != nil {
		return err
	}
	stored, readErr := env.Read(ctx, file)
	if readErr == nil {
		readErr = verify(stored)
	}
	if readErr != nil {
		if existed {
			_ = env.Write(ctx, file, orig, 0o600)
		} else {
			_ = env.Remove(ctx, file)
		}
		return envtarget.Errorf(500, "写入后回读校验失败，已还原原文件：%v", readErr)
	}
	return nil
}

// statusOf maps an error to the HTTP status and message to report.
func statusOf(err error) (int, string) {
	var failure *envtarget.Error
	if errors.As(err, &failure) {
		return failure.Status, failure.Detail
	}
	return http.StatusInternalServerError, err.Error()
}

// cli runs an agent's own command line on the machine.
func (s *Service) cli(ctx context.Context, env envtarget.Env, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	result, err := env.Run(ctx, envtarget.Command{Name: name, Args: args, Env: map[string]string{"NO_COLOR": "1", "CI": "1"}})
	if err != nil {
		return "", err
	}
	out := cleanOutput(result.Stdout, result.Stderr)
	if result.Code != 0 {
		return out, envtarget.Errorf(502, "%s %s 失败：%s", name, firstArg(args), tail(out, 600))
	}
	return out, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
