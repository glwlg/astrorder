package modelconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
)

var credentialNames = []string{EnvTokenKey, MagpieEnvKey}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func validateManagedContent(name string, data []byte) error {
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	switch name {
	case FileCodexCatalog, FileCodexMagpieCatalog:
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return newProtocolErrorf("invalid JSON in %s: %v", name, err)
		}
		return nil
	case FileCodexConfig, FileGrokConfig:
		if err := validateTOML(data); err != nil {
			return newProtocolErrorf("invalid TOML in %s: %v", name, err)
		}
		return nil
	case FileHermesConfig:
		if strings.Contains(string(data), "\x00") {
			return newProtocolErrorf("invalid Hermes config")
		}
		return nil
	default:
		return newProtocolErrorf("unrecognized managed file: %s", name)
	}
}

func validateAPIKey(key string) error {
	for _, r := range key {
		if r == '\r' || r == '\n' || r == 0 {
			return newProtocolErrorf("模型网关 Key 不能包含换行或空字符")
		}
	}
	return nil
}

func formatEnvData(name, key string) ([]byte, error) {
	if _, ok := CredentialFiles[name]; !ok {
		return nil, newProtocolErrorf("模型网关 Key 名称无效")
	}
	if err := validateAPIKey(key); err != nil {
		return nil, err
	}
	escaped := strings.ReplaceAll(key, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return []byte(fmt.Sprintf("%s=\"%s\"\n", name, escaped)), nil
}

func credentialValues(request map[string]any) (map[string]string, error) {
	values := map[string]string{}
	if raw, exists := request["api_keys"]; exists && raw != nil {
		keyed, ok := raw.(map[string]any)
		if !ok {
			return nil, newProtocolErrorf("模型配置写入内容无效")
		}
		for _, name := range credentialNames {
			value, ok := keyed[name].(string)
			if !ok || value == "" {
				continue
			}
			if err := validateAPIKey(value); err != nil {
				return nil, err
			}
			values[name] = value
		}
	}
	if raw, exists := request["api_key"]; exists {
		apiKey, ok := raw.(string)
		if !ok {
			return nil, newProtocolErrorf("模型配置写入内容无效")
		}
		if apiKey != "" {
			if err := validateAPIKey(apiKey); err != nil {
				return nil, err
			}
			if _, exists := values[EnvTokenKey]; !exists {
				values[EnvTokenKey] = apiKey
			}
		}
	}
	return values, nil
}

func isWithinRoot(root, target string) bool {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func resolveWithinAllowed(allowedRoots []string, targetRoot, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", newProtocolErrorf("path contains null character")
	}

	cleanRel := filepath.Clean(rel)
	if filepath.IsAbs(cleanRel) {
		return "", newProtocolErrorf("path escape: absolute paths not allowed: %s", rel)
	}
	if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return "", newProtocolErrorf("path escape: traversal outside root: %s", rel)
	}

	cleanTargetRoot := filepath.Clean(targetRoot)

	rootAllowed := false
	for _, allowed := range allowedRoots {
		if isWithinRoot(allowed, cleanTargetRoot) {
			rootAllowed = true
			break
		}
	}
	if !rootAllowed {
		return "", newProtocolErrorf("path escape: target root %q outside allowed roots", targetRoot)
	}

	fullPath := filepath.Join(cleanTargetRoot, cleanRel)
	if !isWithinRoot(cleanTargetRoot, fullPath) {
		return "", newProtocolErrorf("path escape: resolved path %q outside target root", fullPath)
	}

	resolvedTargetRoot, err := filepath.EvalSymlinks(cleanTargetRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", newProtocolErrorf("eval symlink target root: %v", err)
	}
	if resolvedTargetRoot != "" {
		canonicalRootAllowed := false
		for _, allowed := range allowedRoots {
			resAllowed, _ := filepath.EvalSymlinks(filepath.Clean(allowed))
			if resAllowed == "" {
				resAllowed = filepath.Clean(allowed)
			}
			if isWithinRoot(resAllowed, resolvedTargetRoot) {
				canonicalRootAllowed = true
				break
			}
		}
		if !canonicalRootAllowed {
			return "", newProtocolErrorf("path escape: target root symlink points outside allowed roots")
		}
	}

	curr := cleanTargetRoot
	parts := strings.Split(cleanRel, string(filepath.Separator))
	for _, part := range parts {
		curr = filepath.Join(curr, part)
		fi, lerr := os.Lstat(curr)
		if lerr != nil {
			if errors.Is(lerr, os.ErrNotExist) {
				break
			}
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, evalErr := filepath.EvalSymlinks(curr)
			if evalErr != nil {
				return "", newProtocolErrorf("path escape: symlink error on %q: %v", curr, evalErr)
			}
			symAllowed := false
			for _, allowed := range allowedRoots {
				resAllowed, _ := filepath.EvalSymlinks(filepath.Clean(allowed))
				if resAllowed == "" {
					resAllowed = filepath.Clean(allowed)
				}
				if isWithinRoot(resAllowed, resolved) {
					symAllowed = true
					break
				}
			}
			if !symAllowed {
				return "", newProtocolErrorf("path escape: symlink on %q points outside allowed roots", curr)
			}
			if curr == fullPath {
				return "", newProtocolErrorf("path escape: target file %q is a symlink", curr)
			}
		}
	}

	return fullPath, nil
}

func validateTOML(data []byte) error {
	var document map[string]any
	return toml.Unmarshal(data, &document)
}
