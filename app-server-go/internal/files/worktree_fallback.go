package files

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/targetfs"
)

const (
	headerResolvedPath   = "X-Astrorder-Resolved-Path"
	headerWorktreePath   = "X-Astrorder-Worktree-Path"
	headerWorktreeBranch = "X-Astrorder-Worktree-Branch"

	worktreeCacheTTL = 8 * time.Second
)

type worktreeCacheEntry struct {
	expiresAt time.Time
	topLevel  string
	entries   []targetfs.WorktreeEntryWire
}

type worktreeFallbackMatch struct {
	FileSystem     targetfs.FileSystem
	Kind           string
	ResolvedPath   string
	WorktreePath   string
	WorktreeBranch string
	Info           targetfs.Info
}

type worktreeLookupCache struct {
	mu      sync.Mutex
	entries map[string]worktreeCacheEntry
}

func (c *worktreeLookupCache) get(key string) (worktreeCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return worktreeCacheEntry{}, false
	}
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(c.entries, key)
		}
		return worktreeCacheEntry{}, false
	}
	return entry, true
}

func (c *worktreeLookupCache) set(key string, topLevel string, entries []targetfs.WorktreeEntryWire) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]worktreeCacheEntry)
	}
	if len(c.entries) > 64 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) > 64 {
			c.entries = make(map[string]worktreeCacheEntry)
		}
	}
	c.entries[key] = worktreeCacheEntry{
		expiresAt: time.Now().Add(worktreeCacheTTL),
		topLevel:  topLevel,
		entries:   entries,
	}
}

func isMissingPathOrWorkspaceError(err error) bool {
	if err == nil {
		return false
	}
	if targetfs.IsNotFound(err) {
		return true
	}
	var targetErr *targetfs.Error
	if errors.As(err, &targetErr) {
		return targetErr.Code == "invalid_workspace"
	}
	return false
}

func isWorkspaceOutOfScopeError(err error) bool {
	var targetErr *targetfs.Error
	return errors.As(err, &targetErr) && targetErr.Code == "workspace_out_of_scope"
}

func isPOSIXPath(p string) bool {
	return strings.HasPrefix(strings.TrimSpace(p), "/")
}

func normalizeSlashPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	if isPOSIXPath(trimmed) {
		return path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	}
	return filepath.Clean(trimmed)
}

func sameNormalizedPath(a, b string) bool {
	na := normalizeSlashPath(a)
	nb := normalizeSlashPath(b)
	if na == "" || nb == "" {
		return false
	}
	if isPOSIXPath(na) || isPOSIXPath(nb) {
		return na == nb
	}
	return strings.EqualFold(na, nb)
}

// relativeUnderRoot returns the clean relative path of candidate under root, or ok=false.
func relativeUnderRoot(root, candidate string) (string, bool) {
	cleanRoot := normalizeSlashPath(root)
	cleanCand := normalizeSlashPath(candidate)
	if cleanRoot == "" || cleanCand == "" {
		return "", false
	}
	if isPOSIXPath(cleanRoot) || isPOSIXPath(cleanCand) {
		r := strings.TrimRight(strings.ReplaceAll(cleanRoot, "\\", "/"), "/")
		c := strings.ReplaceAll(cleanCand, "\\", "/")
		if c == r {
			return "", false
		}
		prefix := r + "/"
		if !strings.HasPrefix(c, prefix) {
			return "", false
		}
		rel := strings.TrimPrefix(c, prefix)
		if rel == "" || strings.HasPrefix(rel, "..") || strings.Contains("/"+rel+"/", "/../") {
			return "", false
		}
		return rel, true
	}
	rel, err := filepath.Rel(cleanRoot, cleanCand)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func joinTargetPath(root, relSlash string) string {
	if isPOSIXPath(root) {
		return path.Join(strings.ReplaceAll(root, "\\", "/"), relSlash)
	}
	return filepath.Join(root, filepath.FromSlash(relSlash))
}

func setWorktreeHeaders(response http.ResponseWriter, match worktreeFallbackMatch) {
	if match.ResolvedPath != "" {
		response.Header().Set(headerResolvedPath, url.QueryEscape(match.ResolvedPath))
	}
	if match.WorktreePath != "" {
		response.Header().Set(headerWorktreePath, url.QueryEscape(match.WorktreePath))
	}
	if match.WorktreeBranch != "" {
		response.Header().Set(headerWorktreeBranch, url.QueryEscape(match.WorktreeBranch))
	}
}

func (h *Handler) listLinkedWorktrees(ctx context.Context, resolved targetfs.Resolved, workspace string) (string, []targetfs.WorktreeEntryWire) {
	cleanWS := normalizeSlashPath(workspace)
	if cleanWS == "" {
		return "", nil
	}
	cacheKey := resolved.ConnectionID + "\x00" + cleanWS
	if cached, ok := h.worktrees.get(cacheKey); ok {
		return cached.topLevel, cached.entries
	}

	gitRes, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
		Operation:       targetfs.GitWorktreeList,
		Workspace:       cleanWS,
		IncludeStatuses: false,
	})
	if err != nil || gitRes.ExitCode != 0 || len(gitRes.Stdout) == 0 {
		h.worktrees.set(cacheKey, "", nil)
		return "", nil
	}
	var wire targetfs.WorktreeListWire
	if err := json.Unmarshal(gitRes.Stdout, &wire); err != nil || !wire.IsGitRepo {
		h.worktrees.set(cacheKey, "", nil)
		return "", nil
	}
	topLevel := normalizeSlashPath(wire.Workspace)
	if topLevel == "" {
		topLevel = cleanWS
	}
	h.worktrees.set(cacheKey, topLevel, wire.Worktrees)
	return topLevel, wire.Worktrees
}

// resolveNearestExistingAncestor walks upward from start until Resolver.Resolve finds
// an existing allowed directory (bounded to 12 hops). Non-missing errors (e.g., 403 or
// unknown SSH connection) stop immediately.
func (h *Handler) resolveNearestExistingAncestor(
	ctx context.Context,
	start string,
	sessionID string,
	connectionID string,
) (targetfs.Resolved, string, bool) {
	cur := normalizeSlashPath(start)
	for depth := 0; depth < 12 && cur != ""; depth++ {
		res, err := h.resolve(ctx, cur, sessionID, connectionID)
		if err == nil {
			return res, cur, true
		}
		if !isMissingPathOrWorkspaceError(err) {
			return targetfs.Resolved{}, "", false
		}
		next := fileParent(cur)
		if next == "" || sameNormalizedPath(next, cur) {
			break
		}
		cur = next
	}
	return targetfs.Resolved{}, "", false
}

// resolveLinkedWorktreeScope resolves requestedWorkspace when it is directly inside a
// registered linked Git worktree of the session's repository (used when switching the
// active workspace view to a linked worktree located outside default AllowedRoots).
func (h *Handler) resolveLinkedWorktreeScope(
	ctx context.Context,
	requestedWorkspace string,
	sessionID string,
	connectionID string,
) (targetfs.Resolved, bool) {
	cleanReq := normalizeSlashPath(requestedWorkspace)
	if cleanReq == "" || strings.TrimSpace(sessionID) == "" {
		return targetfs.Resolved{}, false
	}
	baseResolved, err := h.resolve(ctx, "", sessionID, connectionID)
	if err != nil || baseResolved.Workspace == "" {
		return targetfs.Resolved{}, false
	}
	_, entries := h.listLinkedWorktrees(ctx, baseResolved, baseResolved.Workspace)
	for _, entry := range entries {
		if entry.Bare || entry.Prunable || entry.Path == "" {
			continue
		}
		if sameNormalizedPath(cleanReq, entry.Path) {
			wtResolved, resErr := h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
				Workspace:    cleanReq,
				ConnectionID: baseResolved.ConnectionID,
				ExtraRoots:   []string{entry.Path},
			})
			if resErr == nil {
				return wtResolved, true
			}
			return targetfs.Resolved{}, false
		}
		if _, under := relativeUnderRoot(entry.Path, cleanReq); under {
			wtResolved, resErr := h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
				Workspace:    cleanReq,
				ConnectionID: baseResolved.ConnectionID,
				ExtraRoots:   []string{entry.Path},
			})
			if resErr == nil {
				return wtResolved, true
			}
			return targetfs.Resolved{}, false
		}
	}
	return targetfs.Resolved{}, false
}

// resolveWorktreeFallback attempts to locate a missing file in another linked Git worktree
// registered to the same repository. Only linked worktrees returned by `git worktree list`
// are inspected, preserving sandbox/repository boundaries.
func (h *Handler) resolveWorktreeFallback(
	ctx context.Context,
	originalPath string,
	baseWorkspace string,
	sessionID string,
	connectionID string,
) (worktreeFallbackMatch, bool) {
	cleanPath := targetfs.CleanPath(originalPath)
	if cleanPath == "" {
		return worktreeFallbackMatch{}, false
	}

	// Determine the anchor workspace (preferring the session's bound workspace when provided).
	anchorWS := targetfs.CleanPath(baseWorkspace)
	var baseResolved targetfs.Resolved
	var resolveErr error
	if strings.TrimSpace(sessionID) != "" {
		baseResolved, resolveErr = h.resolve(ctx, "", sessionID, connectionID)
		if resolveErr == nil && baseResolved.Workspace != "" {
			anchorWS = baseResolved.Workspace
		}
	}
	if anchorWS == "" {
		anchorWS = fileParent(cleanPath)
	}
	if baseResolved.FileSystem == nil {
		res, existingDir, ok := h.resolveNearestExistingAncestor(ctx, anchorWS, sessionID, connectionID)
		if !ok {
			return worktreeFallbackMatch{}, false
		}
		baseResolved = res
		anchorWS = existingDir
	}

	// GitWorktreeList resolves `rev-parse --show-toplevel` and returns the true repo root in wire.Workspace.
	topLevel, entries := h.listLinkedWorktrees(ctx, baseResolved, anchorWS)
	if topLevel == "" || len(entries) <= 1 {
		return worktreeFallbackMatch{}, false
	}

	relPath, ok := relativeUnderRoot(topLevel, cleanPath)
	if !ok {
		return worktreeFallbackMatch{}, false
	}

	var matches []worktreeFallbackMatch
	for _, entry := range entries {
		if entry.Bare || entry.Prunable || entry.Path == "" {
			continue
		}
		if sameNormalizedPath(entry.Path, topLevel) {
			continue
		}
		wtResolved, err := h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
			Workspace:    entry.Path,
			ConnectionID: baseResolved.ConnectionID,
			ExtraRoots:   []string{entry.Path},
		})
		if err != nil {
			continue
		}
		candidatePath := joinTargetPath(entry.Path, relPath)
		info, statErr := wtResolved.FileSystem.Stat(ctx, candidatePath)
		if statErr != nil || info.IsDir {
			continue
		}
		branch := strings.TrimSpace(entry.Branch)
		if branch == "" && len(entry.Head) >= 7 {
			branch = entry.Head[:7]
		}
		matches = append(matches, worktreeFallbackMatch{
			FileSystem:     wtResolved.FileSystem,
			Kind:           wtResolved.Kind,
			ResolvedPath:   info.Path,
			WorktreePath:   entry.Path,
			WorktreeBranch: branch,
			Info:           info,
		})
	}
	if len(matches) == 0 {
		return worktreeFallbackMatch{}, false
	}
	if len(matches) > 1 {
		// Prefer the most recently modified copy across linked worktrees.
		sort.SliceStable(matches, func(i, j int) bool {
			return matches[i].Info.ModTime.After(matches[j].Info.ModTime)
		})
	}
	return matches[0], true
}
