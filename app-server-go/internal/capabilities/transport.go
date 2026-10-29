package capabilities

import (
	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/envtarget"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path"
	"strings"
)

// TransportProvisioner prepares the connection over the existing trusted
// machine channel. Credentials are never included in skill text or responses.
func TransportProvisioner(port, publicURL, token string) func(context.Context, envtarget.Env) ([]byte, error) {
	return func(ctx context.Context, env envtarget.Env) ([]byte, error) {
		endpoint := "http://127.0.0.1:" + port
		if !env.IsLocal() {
			endpoint = publicURL
			if endpoint == "" {
				result, err := env.Shell(ctx, `if [ -n "$SSH_CONNECTION" ]; then printf '%s' "${SSH_CONNECTION%% *}"; else ip route show default | cut -d ' ' -f 3; fi`, nil)
				if err != nil {
					return nil, err
				}
				host := strings.TrimSpace(result.Stdout)
				if result.Code != 0 || net.ParseIP(host) == nil {
					return nil, envtarget.Errorf(422, "无法自动确定星序宿主机地址")
				}
				endpoint = "http://" + net.JoinHostPort(host, port)
			}
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, envtarget.Errorf(422, "星序服务地址无效")
		}
		if token == "" {
			return nil, envtarget.Errorf(503, "星序服务认证配置不可用")
		}
		return json.Marshal(map[string]string{"url": endpoint, "token": token})
	}
}

func (s *Service) prepareDispatchedCLI(ctx context.Context, dest envtarget.Env, kind string) error {
	registry, ok := s.Agents.(interface {
		ListAgents(context.Context) ([]map[string]any, error)
	})
	if !ok {
		return nil
	} // Isolated adapters without an Agent registry retain strict readiness checks.
	agents, err := registry.ListAgents(ctx)
	if err != nil {
		return err
	}
	conn := dest.Target().ConnectionID
	if conn == "" {
		conn = "local"
	}
	var ids []string
	for _, agent := range agents {
		c, _ := agent["connection_id"].(string)
		if c == "" {
			c = "local"
		}
		if agent["kind"] == kind && c == conn {
			if id, ok := agent["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) > 1 {
		paths, err := dest.Paths(ctx)
		if err != nil {
			return err
		}
		home := skillHome(paths, kind)
		callerPath := path.Join(home, agentskill.CallerFile)
		if err := checkOfficialTarget(ctx, dest, home, callerPath); err != nil {
			return err
		}
		raw, err := dest.Read(ctx, callerPath)
		if err != nil && !errors.Is(err, envtarget.ErrNotExist) {
			return err
		}
		var caller struct {
			AgentID string `json:"agent_id"`
		}
		if json.Unmarshal(raw, &caller) == nil {
			for _, id := range ids {
				if caller.AgentID == id {
					ids = []string{id}
					break
				}
			}
		}
	}
	if len(ids) != 1 {
		return envtarget.Errorf(422, "请先为目标机器上的 %s Agent 配置星序会话 CLI，才能分发技能", kind)
	}
	_, err = s.SessionCLI(ctx, ids[0], true)
	return err
}
