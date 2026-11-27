//go:build windows

package process

import (
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

var environmentReference = regexp.MustCompile(`%([^%]+)%`)

func systemEnvironment() []string {
	var result []string
	for _, source := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		key, err := registry.OpenKey(source.root, source.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, err := key.ReadValueNames(-1)
		if err == nil {
			for _, name := range names {
				if value, _, err := key.GetStringValue(name); err == nil {
					result = append(result, name+"="+value)
				}
			}
		}
		key.Close()
	}
	return result
}

// systemPath reads the latest Windows User and Machine Path values from the
// registry and expands %VAR% references against the current environment.
func systemPath() string {
	readPath := func(root registry.Key, subkey string) string {
		key, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
		defer key.Close()
		val, _, err := key.GetStringValue("Path")
		if err != nil {
			return ""
		}
		return val
	}
	userPath := readPath(registry.CURRENT_USER, `Environment`)
	machinePath := readPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	combined := MergePath(userPath, machinePath)
	if combined == "" {
		return ""
	}
	env := append(os.Environ(), systemEnvironment()...)
	env = append(env, "PATH="+combined)
	expanded := expandEnvironment(env)
	for i := len(expanded) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(expanded[i], "=")
		if ok && strings.EqualFold(k, "PATH") {
			return MergePath(v)
		}
	}
	return combined
}

func expandEnvironment(env []string) []string {
	values := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		values[strings.ToUpper(name)] = value
	}
	for i, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		value = environmentReference.ReplaceAllStringFunc(value, func(token string) string {
			if replacement, ok := values[strings.ToUpper(token[1:len(token)-1])]; ok {
				return replacement
			}
			return token
		})
		env[i] = name + "=" + value
	}
	return env
}
