package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/targetfs"
)

// WorktreeOptions describes optional Git worktree creation or reuse when creating or branching a session.
type WorktreeOptions struct {
	Mode         string   `json:"mode"`                    // "" | "none" | "create" | "existing"
	Branch       string   `json:"branch,omitempty"`        // branch name to create or checkout
	BaseRef      string   `json:"base_ref,omitempty"`      // base commit/branch when creating a new branch
	CreateBranch *bool    `json:"create_branch,omitempty"` // default true when mode == "create"
	Path         string   `json:"path,omitempty"`          // target worktree directory
	Layout       string   `json:"layout,omitempty"`        // "inside" (default) | "sibling"
	CopyFiles    []string `json:"copy_files,omitempty"`    // ignored config files to copy into new worktree
}

func (o *WorktreeOptions) activeMode() string {
	if o == nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(o.Mode))
	if mode == "" || mode == "none" {
		return ""
	}
	return mode
}

type preparedWorktree struct {
	Workspace     string
	RootWorkspace string
	Branch        string
	Managed       bool
	Created       bool
	CreatedBranch bool
	CopiedFiles   []string
	ConnectionID  string
	resolved      targetfs.Resolved
}

func (p *preparedWorktree) isLinked() bool {
	if p == nil || p.RootWorkspace == "" || p.Workspace == "" {
		return false
	}
	return !sameWorkspacePath(p.Workspace, p.RootWorkspace)
}

func sameWorkspacePath(left, right string) bool {
	norm := func(v string) string {
		s := strings.TrimSpace(strings.ReplaceAll(v, "\\", "/"))
		s = strings.TrimRight(s, "/")
		if len(s) >= 2 && s[1] == ':' {
			return strings.ToLower(s)
		}
		return s
	}
	return norm(left) == norm(right)
}

func (h *Handler) prepareSessionWorktree(ctx context.Context, agent map[string]any, sourceWorkspace string, opts *WorktreeOptions) (*preparedWorktree, int, string) {
	mode := opts.activeMode()
	if mode == "" {
		return nil, 0, ""
	}
	if h.resolver == nil {
		return nil, http.StatusServiceUnavailable, "目标文件系统未配置，无法操作 Git Worktree"
	}
	connID := normalizeConnectionID(stringValue(agent, "connection_id"))
	resolved, err := h.resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    targetfs.CleanPath(sourceWorkspace),
		ConnectionID: connID,
	})
	if err != nil {
		statusCode, detail := targetfsErrorStatus(err)
		return nil, statusCode, detail
	}

	switch mode {
	case "create":
		branch := strings.TrimSpace(opts.Branch)
		if branch == "" {
			branch = fmt.Sprintf("wt-%s-%s", time.Now().Format("20060102"), randomHex(2))
		}
		createBranch := true
		if opts.CreateBranch != nil {
			createBranch = *opts.CreateBranch
		}
		res, execErr := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation:  targetfs.GitWorktreeAdd,
			Workspace:  resolved.Workspace,
			Branch:     branch,
			BaseBranch: strings.TrimSpace(opts.BaseRef),
			Create:     createBranch,
			TargetPath: strings.TrimSpace(opts.Path),
			Layout:     strings.TrimSpace(opts.Layout),
			CopyFiles:  opts.CopyFiles,
		})
		if execErr != nil {
			statusCode, detail := targetfsErrorStatus(execErr)
			return nil, statusCode, detail
		}
		var added targetfs.WorktreeAddWire
		if unmarshalErr := json.Unmarshal(res.Stdout, &added); unmarshalErr != nil || added.Path == "" {
			return nil, http.StatusBadGateway, "Worktree 创建结果无效"
		}
		return &preparedWorktree{
			Workspace:     added.Path,
			RootWorkspace: added.MainWorkspace,
			Branch:        added.Branch,
			Managed:       true,
			Created:       true,
			CreatedBranch: added.CreatedBranch,
			CopiedFiles:   added.CopiedFiles,
			ConnectionID:  resolved.ConnectionID,
			resolved:      resolved,
		}, 0, ""

	case "existing":
		targetPath := strings.TrimSpace(opts.Path)
		if targetPath == "" {
			return nil, http.StatusUnprocessableEntity, "复用已有 Worktree 时必须指定目录路径"
		}
		res, execErr := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation:       targetfs.GitWorktreeList,
			Workspace:       resolved.Workspace,
			IncludeStatuses: false,
		})
		if execErr != nil {
			statusCode, detail := targetfsErrorStatus(execErr)
			return nil, statusCode, detail
		}
		var listed targetfs.WorktreeListWire
		if unmarshalErr := json.Unmarshal(res.Stdout, &listed); unmarshalErr != nil || !listed.IsGitRepo {
			return nil, http.StatusUnprocessableEntity, "当前工作区不是 Git 仓库"
		}
		for _, entry := range listed.Worktrees {
			if sameWorkspacePath(entry.Path, targetPath) {
				if entry.Prunable {
					return nil, http.StatusConflict, "该 Worktree 目录已缺失，请先清理或新建 Worktree"
				}
				rootWS := ""
				if !entry.IsMain {
					rootWS = listed.MainWorkspace
				}
				return &preparedWorktree{
					Workspace:     entry.Path,
					RootWorkspace: rootWS,
					Branch:        entry.Branch,
					Managed:       false,
					Created:       false,
					ConnectionID:  resolved.ConnectionID,
					resolved:      resolved,
				}, 0, ""
			}
		}
		return nil, http.StatusNotFound, "未找到指定的 Git Worktree 目录"

	default:
		return nil, http.StatusUnprocessableEntity, "不支持的 Worktree 模式: " + opts.Mode
	}
}

func (h *Handler) rollbackPreparedWorktree(ctx context.Context, prepared *preparedWorktree) {
	if prepared == nil || !prepared.Created || prepared.resolved.FileSystem == nil || prepared.Workspace == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, err := prepared.resolved.FileSystem.ExecGit(cleanupCtx, targetfs.GitRequest{
		Operation:    targetfs.GitWorktreeRemove,
		Workspace:    prepared.RootWorkspace,
		TargetPath:   prepared.Workspace,
		Force:        true,
		DeleteBranch: prepared.CreatedBranch,
	})
	if err != nil {
		slog.Warn("worktree rollback failed after session creation error", "path", prepared.Workspace, "error", err)
	}
}

func (h *Handler) notifyWorktreesChanged(ctx context.Context, prepared *preparedWorktree) {
	if prepared == nil || !prepared.Created || h.publisher == nil {
		return
	}
	h.publishEvent(ctx, "git.worktrees.changed", map[string]any{
		"workspace":     prepared.RootWorkspace,
		"connection_id": prepared.ConnectionID,
	})
}

func (h *Handler) removeSessionWorktree(ctx context.Context, session map[string]any, force, deleteBranch bool) (removed bool, branchDeleted bool, errMsg string) {
	if h.resolver == nil {
		return false, false, "目标文件系统未配置"
	}
	workspace := strings.TrimSpace(stringValue(session, "workspace"))
	rootWorkspace := strings.TrimSpace(stringValue(session, "worktree_root_workspace"))
	if workspace == "" {
		return false, false, "会话没有工作区路径"
	}
	if rootWorkspace != "" && sameWorkspacePath(workspace, rootWorkspace) {
		return false, false, "主工作区不可移除"
	}
	connID := normalizeConnectionID(stringValue(session, "connection_id"))
	anchor := rootWorkspace
	if anchor == "" {
		anchor = workspace
	}
	resolved, err := h.resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    targetfs.CleanPath(anchor),
		ConnectionID: connID,
	})
	if err != nil {
		_, detail := targetfsErrorStatus(err)
		return false, false, detail
	}
	res, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
		Operation:    targetfs.GitWorktreeRemove,
		Workspace:    resolved.Workspace,
		TargetPath:   workspace,
		Force:        force,
		DeleteBranch: deleteBranch,
	})
	if err != nil {
		_, detail := targetfsErrorStatus(err)
		return false, false, detail
	}
	var out targetfs.WorktreeRemoveWire
	if unmarshalErr := json.Unmarshal(res.Stdout, &out); unmarshalErr != nil {
		return false, false, "无法解析 Worktree 移除结果"
	}
	if out.Removed {
		h.publishEvent(ctx, "git.worktrees.changed", map[string]any{
			"workspace":     anchor,
			"connection_id": resolved.ConnectionID,
		})
	}
	return out.Removed, out.BranchDeleted, out.BranchDeleteError
}

func targetfsErrorStatus(err error) (int, string) {
	var targetErr *targetfs.Error
	if errors.As(err, &targetErr) && targetErr.Status > 0 {
		return targetErr.Status, targetErr.Detail
	}
	return http.StatusBadGateway, err.Error()
}

func defaultWorktreeOptionsFromLegacy(createWorktree bool, branch, path, baseRef, layout string, copyFiles []string, explicit *WorktreeOptions) *WorktreeOptions {
	if explicit != nil && explicit.activeMode() != "" {
		return explicit
	}
	if !createWorktree {
		return nil
	}
	createBranch := true
	return &WorktreeOptions{
		Mode:         "create",
		Branch:       branch,
		BaseRef:      baseRef,
		CreateBranch: &createBranch,
		Path:         path,
		Layout:       layout,
		CopyFiles:    copyFiles,
	}
}
