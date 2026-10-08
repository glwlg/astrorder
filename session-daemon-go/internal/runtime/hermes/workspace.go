package hermes

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	platform "runtime"
	"strings"
)

func (a *Adapter) validateWorkspace(raw string) (string, error) {
	if raw == "" {
		raw = a.config.Workspace
	}
	if raw == "" {
		return "", fmt.Errorf("hermes workspace is required")
	}

	// 远程 SSH 模式下：跳过本地 filepath.Abs / os.Stat 校验，仅做 POSIX 路径清理与 allowlist 校验
	if a.sshStarter != nil {
		if strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") {
			return "", fmt.Errorf("remote hermes workspace must be an absolute POSIX path")
		}
		clean := path.Clean(raw)
		if !a.isAllowedRemote(clean) {
			return "", fmt.Errorf("hermes workspace is outside the allowlist")
		}
		return clean, nil
	}

	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("hermes workspace is invalid")
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !a.isAllowed(eval) {
		return "", fmt.Errorf("hermes workspace is outside the allowlist")
	}
	return eval, nil
}

func (a *Adapter) isAllowedRemote(dir string) bool {
	roots := make([]string, 0, len(a.config.Allowed)+1)
	if a.config.Workspace != "" {
		roots = append(roots, a.config.Workspace)
	}
	roots = append(roots, a.config.Allowed...)

	if len(roots) == 0 {
		return true
	}

	for _, root := range roots {
		if root == "" {
			continue
		}
		cleanRoot := path.Clean(root)
		if cleanRoot == "/" {
			return true
		}
		if dir == cleanRoot || strings.HasPrefix(dir, cleanRoot+"/") {
			return true
		}
	}
	return false
}

func (a *Adapter) isAllowed(target string) bool {
	roots := make([]string, 0, len(a.config.Allowed)+1)
	if a.config.Workspace != "" {
		roots = append(roots, a.config.Workspace)
	}
	roots = append(roots, a.config.Allowed...)

	for _, root := range roots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		evalRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			evalRoot = absRoot
		}

		candidate := target
		base := evalRoot
		if platform.GOOS == "windows" {
			candidate = strings.ToLower(candidate)
			base = strings.ToLower(base)
		}
		rel, err := filepath.Rel(base, candidate)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}
