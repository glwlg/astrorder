package pluginhost

import (
	"context"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

const maxGitMetaFileBytes = 512 << 10

// ParseGitRemoteURL extracts (host, port, projectPath) from HTTPS, HTTP, SSH, git://,
// or SCP-style git remotes.
func ParseGitRemoteURL(raw string) (host, port, projectPath string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", "", false
	}

	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Hostname() == "" {
			return "", "", "", false
		}
		cleanPath := cleanGitProjectPath(parsed.Path)
		if cleanPath == "" {
			return "", "", "", false
		}
		return strings.ToLower(parsed.Hostname()), parsed.Port(), cleanPath, true
	}

	colonIdx := strings.Index(trimmed, ":")
	if colonIdx <= 0 {
		return "", "", "", false
	}
	left := trimmed[:colonIdx]
	right := trimmed[colonIdx+1:]
	if strings.ContainsAny(left, `/\`) {
		return "", "", "", false
	}
	if atIdx := strings.LastIndex(left, "@"); atIdx >= 0 {
		left = left[atIdx+1:]
	}
	left = strings.Trim(left, "[]")
	if left == "" {
		return "", "", "", false
	}
	cleanPath := cleanGitProjectPath(right)
	if cleanPath == "" {
		return "", "", "", false
	}
	return strings.ToLower(left), "", cleanPath, true
}

func cleanGitProjectPath(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.TrimLeft(p, "/")
	p = strings.TrimRight(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimRight(p, "/")
	if !strings.Contains(p, "/") {
		return ""
	}
	return p
}

// DetectWorkspaceGitInfo inspects <workspace>/.git (both standard repos and worktrees)
// via targetfs so it works across Local, WSL, and SSH remote workspaces.
func DetectWorkspaceGitInfo(ctx context.Context, resolver *targetfs.Resolver, workspace, connectionID, sessionID string) (*WorkspaceGitInfo, error) {
	if resolver == nil || strings.TrimSpace(workspace) == "" {
		return nil, nil
	}
	resolved, err := resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    targetfs.CleanPath(workspace),
		ConnectionID: strings.TrimSpace(connectionID),
		SessionID:    strings.TrimSpace(sessionID),
	})
	if err != nil {
		return nil, err
	}

	posix := isPOSIXTarget(resolved.ConnectionID, resolved.Workspace)
	gitDir, commonDir, err := resolveGitDirs(ctx, resolver, resolved, posix)
	if err != nil || gitDir == "" {
		return nil, nil
	}

	gitFS := resolveDirFS(ctx, resolver, resolved, gitDir)
	commonFS := gitFS
	if commonDir != gitDir {
		commonFS = resolveDirFS(ctx, resolver, resolved, commonDir)
	}

	configContent, ok := readSmallGitFile(ctx, commonFS, joinGitPath(posix, commonDir, "config"))
	if !ok {
		return nil, nil
	}
	remotes := parseGitConfigRemotes(configContent)

	var pickedName, pickedURL, host, port, projectPath string
	for _, name := range []string{"origin", "upstream"} {
		if u, exists := remotes[name]; exists {
			if h, p, proj, valid := ParseGitRemoteURL(u); valid {
				pickedName, pickedURL, host, port, projectPath = name, sanitizeRemoteURL(u), h, p, proj
				break
			}
		}
	}
	if projectPath == "" && len(remotes) > 0 {
		names := make([]string, 0, len(remotes))
		for name := range remotes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			u := remotes[name]
			if h, p, proj, valid := ParseGitRemoteURL(u); valid {
				pickedName, pickedURL, host, port, projectPath = name, sanitizeRemoteURL(u), h, p, proj
				break
			}
		}
	}

	branch, sha := readGitHeadAndSHA(ctx, gitFS, commonFS, posix, gitDir, commonDir)
	if branch == "" {
		if brRes, brErr := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitBranches,
			Workspace: resolved.Workspace,
		}); brErr == nil && brRes.ExitCode == 0 {
			branch = extractCurrentBranchFromOutput(string(brRes.Stdout))
		}
	}

	if projectPath == "" && branch == "" && sha == "" {
		return nil, nil
	}

	return &WorkspaceGitInfo{
		Host:        host,
		Port:        port,
		ProjectPath: projectPath,
		Branch:      branch,
		HeadSHA:     sha,
		RemoteName:  pickedName,
		RemoteURL:   pickedURL,
	}, nil
}

func resolveDirFS(ctx context.Context, resolver *targetfs.Resolver, base targetfs.Resolved, dir string) targetfs.FileSystem {
	if dir == "" || resolver == nil {
		return base.FileSystem
	}
	if alt, err := resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    dir,
		ConnectionID: base.ConnectionID,
	}); err == nil && alt.FileSystem != nil {
		return alt.FileSystem
	}
	return base.FileSystem
}

func resolveGitDirs(ctx context.Context, resolver *targetfs.Resolver, base targetfs.Resolved, posix bool) (gitDir, commonDir string, err error) {
	fs := base.FileSystem
	workspace := base.Workspace
	dotGit := joinGitPath(posix, workspace, ".git")
	info, statErr := fs.Stat(ctx, dotGit)
	if statErr != nil {
		return "", "", statErr
	}
	if info.IsDir {
		return dotGit, dotGit, nil
	}
	raw, ok := readSmallGitFile(ctx, fs, dotGit)
	if !ok {
		return "", "", nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "gitdir:") {
			target := strings.TrimSpace(trimmed[len("gitdir:"):])
			if target == "" {
				return "", "", nil
			}
			if !isAbsGitPath(target) {
				target = joinGitPath(posix, workspace, target)
			}
			gitDir = target
			break
		}
	}
	if gitDir == "" {
		return "", "", nil
	}
	commonDir = gitDir
	gitFS := resolveDirFS(ctx, resolver, base, gitDir)
	if rawCommon, hasCommon := readSmallGitFile(ctx, gitFS, joinGitPath(posix, gitDir, "commondir")); hasCommon {
		rel := strings.TrimSpace(rawCommon)
		if rel != "" {
			if isAbsGitPath(rel) {
				commonDir = rel
			} else {
				commonDir = joinGitPath(posix, gitDir, rel)
			}
		}
	}
	return gitDir, commonDir, nil
}

func readGitHeadAndSHA(ctx context.Context, gitFS, commonFS targetfs.FileSystem, posix bool, gitDir, commonDir string) (branch, sha string) {
	headRaw, ok := readSmallGitFile(ctx, gitFS, joinGitPath(posix, gitDir, "HEAD"))
	if !ok {
		return "", ""
	}
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(headRaw, "\r\n", "\n"), "\n")[0])
	if strings.HasPrefix(line, "ref:") {
		refName := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		branch = strings.TrimPrefix(refName, "refs/heads/")
		if refContent, found := readSmallGitFile(ctx, commonFS, joinGitPath(posix, commonDir, refName)); found {
			sha = strings.TrimSpace(refContent)
		} else if refContent, found := readSmallGitFile(ctx, gitFS, joinGitPath(posix, gitDir, refName)); found {
			sha = strings.TrimSpace(refContent)
		} else if packed, found := readSmallGitFile(ctx, commonFS, joinGitPath(posix, commonDir, "packed-refs")); found {
			sha = lookupPackedRef(packed, refName)
		}
		return branch, sha
	}
	if len(line) >= 7 && len(line) <= 64 {
		return "", line
	}
	return "", ""
}

func lookupPackedRef(packed, refName string) string {
	for _, raw := range strings.Split(strings.ReplaceAll(packed, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == refName {
			return fields[0]
		}
	}
	return ""
}

func parseGitConfigRemotes(content string) map[string]string {
	remotes := map[string]string{}
	currentRemote := ""
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentRemote = ""
			section := strings.TrimSpace(line[1 : len(line)-1])
			lower := strings.ToLower(section)
			if strings.HasPrefix(lower, "remote ") {
				rest := strings.TrimSpace(section[len("remote "):])
				if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
					currentRemote = rest[1 : len(rest)-1]
				}
			}
			continue
		}
		if currentRemote == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		if key == "url" && val != "" {
			if _, already := remotes[currentRemote]; !already {
				remotes[currentRemote] = val
			}
		}
	}
	return remotes
}

func sanitizeRemoteURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "://") {
		return trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	u.User = nil
	return u.String()
}

func extractCurrentBranchFromOutput(stdout string) string {
	for _, raw := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "*") {
			b := strings.TrimSpace(strings.TrimPrefix(line, "*"))
			if strings.HasPrefix(b, "(HEAD detached") {
				return ""
			}
			return b
		}
	}
	return ""
}

func readSmallGitFile(ctx context.Context, fs targetfs.FileSystem, filePath string) (string, bool) {
	res, err := fs.Read(ctx, filePath)
	if err != nil || res == nil || res.Reader == nil {
		return "", false
	}
	defer res.Reader.Close()
	data, err := io.ReadAll(io.LimitReader(res.Reader, maxGitMetaFileBytes))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func isPOSIXTarget(connectionID, workspace string) bool {
	cid := strings.TrimSpace(connectionID)
	if cid != "" && !strings.EqualFold(cid, targetfs.LocalConnectionID) {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(workspace), "/")
}

func joinGitPath(posix bool, parts ...string) string {
	if posix {
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			cleaned = append(cleaned, strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
		}
		return path.Join(cleaned...)
	}
	return filepath.Join(parts...)
}

func isAbsGitPath(p string) bool {
	t := strings.TrimSpace(p)
	if strings.HasPrefix(t, "/") || strings.HasPrefix(t, `\\`) {
		return true
	}
	return len(t) >= 3 && t[1] == ':' && (t[2] == '\\' || t[2] == '/')
}
