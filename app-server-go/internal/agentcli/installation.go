package agentcli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// installationEnvironment locates only the installation adjacent to this
// executable. It never searches cwd or another runtime's profile.
func installationEnvironment(lookup EnvLookup, executable string, read func(string) ([]byte, error)) (EnvLookup, error) {
	home := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(executable))))
	if filepath.Base(filepath.Dir(executable)) != "scripts" || filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(executable)))) != "skills" {
		return lookup, nil
	}
	raw, err := read(filepath.Join(home, "astrorder-transport.json"))
	if errors.Is(err, os.ErrNotExist) {
		return lookup, nil
	}
	if err != nil {
		return nil, errors.New("无法读取星序连接配置，请重新分发技能")
	}
	var transport struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &transport) != nil || transport.URL == "" || transport.Token == "" {
		return nil, errors.New("星序连接配置不完整，请重新分发技能")
	}
	defaults := map[string]string{"ASTRORDER_AGENT_URL": transport.URL, "ASTRORDER_AGENT_TOKEN": transport.Token, "ASTRORDER_CALLER_CONFIG": filepath.Join(home, "astrorder-caller.json")}
	return func(key string) (string, bool) {
		if v, ok := lookup(key); ok {
			return v, true
		}
		v, ok := defaults[key]
		return v, ok
	}, nil
}
