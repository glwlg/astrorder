package ssh

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

var providerKeys = map[string]bool{
	"OPENCODEX_API_AUTH_TOKEN": true, "MAGPIE_API_KEY": true,
	"STARSHIP_SESSION_KEY": true, "GROK45_API_KEY": true, "EMBED__API_KEY": true,
}

func environmentKeys(env map[string]string) ([]string, error) {
	keys := make([]string, 0, len(env))
	size := 0
	for key, value := range env {
		if !providerKeys[key] || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid remote provider environment")
		}
		size += len(value)
		if size > 32768 {
			return nil, fmt.Errorf("remote provider environment exceeds limit")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func environmentBootstrap(env map[string]string) ([]byte, error) {
	keys, err := environmentKeys(env)
	if err != nil {
		return nil, err
	}
	var data strings.Builder
	for _, key := range keys {
		data.WriteString(base64.StdEncoding.EncodeToString([]byte(env[key])))
		data.WriteByte('\n')
	}
	return []byte(data.String()), nil
}

// Read only the bootstrap lines, leaving all native RPC bytes on stdin intact.
// Values never appear in the SSH command line. Existing remote values win.
func environmentCommand(env map[string]string) (string, error) {
	keys, err := environmentKeys(env)
	if err != nil {
		return "", err
	}
	var script strings.Builder
	for _, key := range keys {
		script.WriteString("IFS= read -r _astrorder_encoded || exit 125; ")
		script.WriteString("_astrorder_value=$(printf '%s' \"$_astrorder_encoded\" | base64 -d; printf '.') || exit 125; ")
		fmt.Fprintf(&script, "if [ -z \"${%s:-}\" ]; then export %s=\"${_astrorder_value%%.}\"; fi; ", key, key)
	}
	if len(keys) > 0 {
		script.WriteString("unset _astrorder_encoded _astrorder_value; ")
	}
	return script.String(), nil
}
