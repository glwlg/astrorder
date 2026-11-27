package toolchain

import (
	"context"
	"errors"
	"strings"

	"astrorder.dev/app-server/internal/daemonclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrDaemonReloadUnsupported indicates the connected Session Daemon is an older
// build that does not yet implement hot config reloading.
var ErrDaemonReloadUnsupported = daemonclient.ErrReloadUnsupported

// DaemonReloader allows overriding or adapting daemon hot-reload invocation
// (both for connecting daemonclient.ReloadConfigWithOptions and unit testing fallback paths).
type DaemonReloader interface {
	ReloadConfig(ctx context.Context) (reloaded bool, detail string, err error)
}

// DaemonReloadResult summarizes an attempted non-fatal hot reload of the Session Daemon.
type DaemonReloadResult struct {
	Attempted   bool   `json:"attempted"`
	Reloaded    bool   `json:"reloaded"`
	Unsupported bool   `json:"unsupported,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// SetDaemonClient updates the live daemon client atomically (used when the App
// server starts in degraded mode and reconnects to the Session Daemon in the background).
func (s *Service) SetDaemonClient(client *daemonclient.Client) {
	s.daemonMu.Lock()
	s.daemon = client
	s.daemonMu.Unlock()
}

func (s *Service) getDaemonClient() *daemonclient.Client {
	s.daemonMu.RLock()
	c := s.daemon
	s.daemonMu.RUnlock()
	return c
}

// tryReloadDaemonConfig triggers a non-fatal Session Daemon hot reload.
// Against older daemons (daemonclient.ErrReloadUnsupported / codes.Unimplemented)
// or disconnected daemons, it degrades gracefully instead of returning an error.
func (s *Service) tryReloadDaemonConfig(ctx context.Context) DaemonReloadResult {
	reloader := s.daemonReloader
	if reloader == nil {
		if client := s.getDaemonClient(); client != nil {
			reloader = clientReloadAdapter{
				client:        client,
				extraPathDirs: ManagedRuntimeBinDirs(s.runtimesRoot()),
			}
		}
	}
	if reloader == nil {
		return DaemonReloadResult{
			Attempted:   true,
			Unavailable: true,
			Detail:      "小内核暂未连接，配置已保存；待小内核恢复连接或手动重启后生效",
		}
	}
	reloaded, detail, err := reloader.ReloadConfig(ctx)
	if err == nil && reloaded {
		if strings.TrimSpace(detail) == "" {
			detail = "已自动热重载小内核配置，新引擎立即生效"
		}
		return DaemonReloadResult{
			Attempted: true,
			Reloaded:  true,
			Detail:    detail,
		}
	}
	if err == nil && !reloaded {
		if strings.TrimSpace(detail) == "" {
			detail = "当前运行中的旧版小内核暂不支持热重载，配置已保存；请在系统托盘「小内核 → 更新小内核」升级或重启后生效"
		}
		return DaemonReloadResult{
			Attempted:   true,
			Unsupported: true,
			Detail:      detail,
		}
	}
	if isReloadUnsupportedError(err) {
		return DaemonReloadResult{
			Attempted:   true,
			Unsupported: true,
			Detail:      "当前运行中的旧版小内核暂不支持热重载，配置已保存；可在系统托盘「小内核 → 更新小内核」升级或重启小内核后生效",
		}
	}
	return DaemonReloadResult{
		Attempted:   true,
		Unavailable: true,
		Detail:      "小内核热重载暂未成功（" + err.Error() + "），配置已保存；可稍后在系统托盘重启小内核生效",
	}
}

func isReloadUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, daemonclient.ErrReloadUnsupported) {
		return true
	}
	if status.Code(err) == codes.Unimplemented {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "unimplemented") || strings.Contains(lower, "unsupported")
}

// clientReloadAdapter bridges *daemonclient.Client to DaemonReloader using
// daemonclient.ReloadConfigWithOptions and gracefully handles ErrReloadUnsupported.
type clientReloadAdapter struct {
	client        *daemonclient.Client
	extraPathDirs []string
}

func (a clientReloadAdapter) ReloadConfig(ctx context.Context) (bool, string, error) {
	if a.client == nil {
		return false, "", errors.New("daemon client not connected")
	}
	probe := true
	res, err := a.client.ReloadConfigWithOptions(ctx, daemonclient.ReloadConfigOptions{
		ProbePath:     &probe,
		ExtraPathDirs: a.extraPathDirs,
	})
	if errors.Is(err, daemonclient.ErrReloadUnsupported) || (res != nil && !res.Supported) {
		return false, "", daemonclient.ErrReloadUnsupported
	}
	if err != nil {
		return false, "", err
	}
	detail := "已自动热重载小内核配置，新引擎立即生效"
	if res != nil && len(res.AddedRuntimes) > 0 {
		detail = "已热重载小内核并接入引擎：" + strings.Join(res.AddedRuntimes, ", ")
	}
	return true, detail, nil
}
