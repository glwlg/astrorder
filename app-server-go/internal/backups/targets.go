package backups

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/targetfs"
)

const maxBrowseEntries = 500

func strValue(m map[string]any, key string) string {
	if m == nil || m[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(m[key]))
}

// ListTargets returns local, configured SSH connections, and available WSL distributions.
func (s *Service) ListTargets(ctx context.Context) (TargetsResponse, error) {
	items := []Target{
		{
			ID:           targetfs.LocalConnectionID,
			ConnectionID: targetfs.LocalConnectionID,
			Kind:         "local",
			Name:         "本机",
			State:        "connected",
			Workspace:    s.defaultWorkspace,
		},
	}

	var sshRows []map[string]any
	if s.store != nil {
		rows, err := s.store.ListSSHConnections(ctx)
		if err != nil {
			return TargetsResponse{}, err
		}
		sshRows = rows
	}

	for _, conn := range sshRows {
		id := strValue(conn, "id")
		if id == "" {
			continue
		}
		name := strValue(conn, "display_name")
		if name == "" {
			name = strValue(conn, "profile_name")
		}
		if name == "" {
			name = id
		}
		settings, _ := conn["settings"].(map[string]any)
		state := strValue(conn, "state")
		if state == "" {
			state = "disconnected"
		}
		items = append(items, Target{
			ID:           id,
			ConnectionID: id,
			Kind:         "ssh",
			Name:         name,
			State:        state,
			Detail:       strValue(conn, "detail"),
			Workspace:    strValue(settings, "workspace"),
		})
	}

	// A configured SSH target can coexist with a native WSL target.
	if runtime.GOOS == "windows" {
		for _, distro := range s.detectWSLDistros(ctx) {
			id := "wsl:" + distro
			items = append(items, Target{
				ID:           id,
				ConnectionID: id,
				Kind:         "wsl",
				Name:         "WSL · " + distro,
				State:        "connected",
			})
		}
	}

	defaultDest := strings.TrimSpace(s.defaultDestinationDir)
	if defaultDest == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			defaultDest = filepath.Join(home, "AstrorderBackups")
		}
	}

	return TargetsResponse{
		Items:                 items,
		DefaultDestinationDir: defaultDest,
	}, nil
}

func (s *Service) detectWSLDistros(ctx context.Context) []string {
	bin := strings.TrimSpace(s.wslBinary)
	if bin == "" {
		bin = "wsl.exe"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, bin, "-l", "-q")
	configureChildProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	distros := make([]string, 0)
	for _, line := range strings.Split(string(out), "\n") {
		clean := strings.TrimSpace(strings.ReplaceAll(line, "\x00", ""))
		if clean != "" {
			distros = append(distros, clean)
		}
	}
	return distros
}

// BrowsePath lists one directory level on a local or remote target.
func (s *Service) BrowsePath(ctx context.Context, connectionID, requestedPath string) (BrowseResponse, error) {
	connID := strings.TrimSpace(connectionID)
	if connID == "" {
		connID = targetfs.LocalConnectionID
	}
	trimmed := strings.TrimSpace(requestedPath)
	if trimmed != "" && hasInvalidControlOrTraversal(trimmed) {
		return BrowseResponse{}, policyError(422, "路径包含非法字符或不允许的 .. 穿越段。")
	}

	if connID == targetfs.LocalConnectionID {
		return s.browseLocal(trimmed)
	}
	return s.browseRemote(ctx, connID, trimmed)
}

func (s *Service) browseLocal(trimmed string) (BrowseResponse, error) {
	root := trimmed
	if root == "" {
		if s.defaultWorkspace != "" {
			root = s.defaultWorkspace
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = home
		} else if runtime.GOOS == "windows" {
			root = `C:\`
		} else {
			root = "/"
		}
	}
	cleaned, err := cleanLocalPath(root)
	if err != nil {
		return BrowseResponse{}, err
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return BrowseResponse{}, policyError(404, "指定目录不存在。")
		}
		return BrowseResponse{}, err
	}
	if !info.IsDir() {
		cleaned = filepath.Dir(cleaned)
	}
	rows, err := os.ReadDir(cleaned)
	if err != nil {
		return BrowseResponse{}, fmt.Errorf("读取目录失败: %w", err)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IsDir() != rows[j].IsDir() {
			return rows[i].IsDir()
		}
		return strings.ToLower(rows[i].Name()) < strings.ToLower(rows[j].Name())
	})
	items := make([]BrowseEntry, 0, len(rows))
	for _, row := range rows {
		if len(items) >= maxBrowseEntries {
			break
		}
		name := row.Name()
		isDir := row.IsDir()
		var size int64
		if !isDir {
			if fi, fiErr := row.Info(); fiErr == nil {
				size = fi.Size()
			}
		}
		items = append(items, BrowseEntry{
			Name:  name,
			Path:  filepath.Join(cleaned, name),
			IsDir: isDir,
			Size:  size,
		})
	}
	var parent *string
	p := filepath.Dir(cleaned)
	if p != cleaned {
		parent = &p
	}
	drives := localWindowsDrives()
	return BrowseResponse{
		ConnectionID: targetfs.LocalConnectionID,
		Root:         cleaned,
		Parent:       parent,
		Drives:       drives,
		Items:        items,
	}, nil
}

func localWindowsDrives() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}
	drives := make([]string, 0)
	for c := 'C'; c <= 'Z'; c++ {
		drive := string(c) + `:\`
		if _, err := os.Stat(drive); err == nil {
			drives = append(drives, drive)
		}
	}
	return drives
}

func (s *Service) browseRemote(ctx context.Context, connID, trimmed string) (BrowseResponse, error) {
	if s.resolver == nil {
		return BrowseResponse{}, policyError(503, "远程文件系统不可用。")
	}
	dir := trimmed
	if dir == "" {
		dir = "/"
		if s.store != nil && !strings.HasPrefix(connID, "wsl:") {
			if conn, err := s.store.GetSSHConnection(ctx, connID); err == nil && conn != nil {
				settings, _ := conn["settings"].(map[string]any)
				if ws := strValue(settings, "workspace"); ws != "" && strings.HasPrefix(ws, "/") {
					dir = ws
				} else if user := strValue(settings, "user"); user == "root" {
					dir = "/root"
				} else if user != "" {
					dir = "/home/" + user
				}
			}
		}
	}
	cleaned, err := cleanPOSIXAbs(dir)
	if err != nil {
		return BrowseResponse{}, err
	}
	resolved, err := s.resolver.ResolveScopedRemote(ctx, connID, cleaned, nil)
	if err != nil && trimmed == "" && cleaned != "/" {
		cleaned = "/"
		resolved, err = s.resolver.ResolveScopedRemote(ctx, connID, cleaned, nil)
	}
	if err != nil {
		return BrowseResponse{}, err
	}
	listRes, err := resolved.FileSystem.List(ctx, targetfs.ListRequest{Path: cleaned, Depth: 1})
	if err != nil {
		return BrowseResponse{}, err
	}
	items := make([]BrowseEntry, 0, len(listRes.Items))
	for _, entry := range listRes.Items {
		if len(items) >= maxBrowseEntries {
			break
		}
		items = append(items, BrowseEntry{
			Name:  entry.Name,
			Path:  filepath.ToSlash(entry.Path),
			IsDir: entry.IsDir,
			Size:  entry.Size,
		})
	}
	var parent *string
	if cleaned != "/" {
		p := path.Dir(cleaned)
		if p != cleaned {
			parent = &p
		}
	}
	return BrowseResponse{
		ConnectionID: connID,
		Root:         cleaned,
		Parent:       parent,
		Drives:       []string{"/"},
		Items:        items,
	}, nil
}
