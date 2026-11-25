package git

import (
	"encoding/json"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

func (h *Handler) publishWorktreesChanged(request *http.Request, resolved targetfs.Resolved, workspace string) {
	if h.options.Publisher == nil {
		return
	}
	if workspace == "" {
		workspace = resolved.Workspace
	}
	_ = h.options.Publisher.Publish(request.Context(), "git.worktrees.changed", map[string]any{
		"workspace":     workspace,
		"connection_id": resolved.ConnectionID,
	})
}

func (h *Handler) listWorktrees(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	resolved, err := h.resolve(request, query.Get("workspace"), query.Get("session_id"), query.Get("connection_id"))
	if err != nil {
		return err
	}
	includeStatuses := query.Get("include_statuses") != "0" && query.Get("include_statuses") != "false"
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation:       targetfs.GitWorktreeList,
		Workspace:       resolved.Workspace,
		IncludeStatuses: includeStatuses,
	})
	if err != nil {
		return err
	}
	var payload targetfs.WorktreeListWire
	if err := json.Unmarshal(res.Stdout, &payload); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_list", Detail: "无法解析 Git Worktree 列表", Err: err}
	}
	writeJSON(response, http.StatusOK, payload)
	return nil
}

func (h *Handler) createWorktree(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Workspace    string   `json:"workspace"`
		SessionID    string   `json:"session_id"`
		ConnectionID string   `json:"connection_id"`
		Branch       string   `json:"branch"`
		BaseRef      string   `json:"base_ref"`
		CreateBranch *bool    `json:"create_branch"`
		TargetPath   string   `json:"target_path"`
		Layout       string   `json:"layout"`
		CopyFiles    []string `json:"copy_files"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	resolved, err := h.resolve(request, body.Workspace, body.SessionID, body.ConnectionID)
	if err != nil {
		return err
	}
	createBranch := true
	if body.CreateBranch != nil {
		createBranch = *body.CreateBranch
	}
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation:  targetfs.GitWorktreeAdd,
		Workspace:  resolved.Workspace,
		Branch:     strings.TrimSpace(body.Branch),
		BaseBranch: strings.TrimSpace(body.BaseRef),
		Create:     createBranch,
		TargetPath: strings.TrimSpace(body.TargetPath),
		Layout:     strings.TrimSpace(body.Layout),
		CopyFiles:  body.CopyFiles,
	})
	if err != nil {
		return err
	}
	var out targetfs.WorktreeAddWire
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_add", Detail: "无法解析 Worktree 创建结果", Err: err}
	}
	h.publishWorktreesChanged(request, resolved, out.MainWorkspace)
	writeJSON(response, http.StatusOK, out)
	return nil
}

func (h *Handler) removeWorktree(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Workspace    string `json:"workspace"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
		TargetPath   string `json:"target_path"`
		Force        bool   `json:"force"`
		DeleteBranch bool   `json:"delete_branch"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	resolved, err := h.resolve(request, body.Workspace, body.SessionID, body.ConnectionID)
	if err != nil {
		return err
	}
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation:    targetfs.GitWorktreeRemove,
		Workspace:    resolved.Workspace,
		TargetPath:   strings.TrimSpace(body.TargetPath),
		Force:        body.Force,
		DeleteBranch: body.DeleteBranch,
	})
	if err != nil {
		return err
	}
	var out targetfs.WorktreeRemoveWire
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_remove", Detail: "无法解析 Worktree 移除结果", Err: err}
	}
	h.publishWorktreesChanged(request, resolved, resolved.Workspace)
	writeJSON(response, http.StatusOK, out)
	return nil
}

func (h *Handler) pruneWorktrees(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Workspace    string `json:"workspace"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	resolved, err := h.resolve(request, body.Workspace, body.SessionID, body.ConnectionID)
	if err != nil {
		return err
	}
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation: targetfs.GitWorktreePrune,
		Workspace: resolved.Workspace,
	})
	if err != nil {
		return err
	}
	var out targetfs.WorktreePruneWire
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_prune", Detail: "无法解析 Worktree 清理结果", Err: err}
	}
	h.publishWorktreesChanged(request, resolved, resolved.Workspace)
	writeJSON(response, http.StatusOK, out)
	return nil
}

func (h *Handler) mergeWorktree(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Workspace    string `json:"workspace"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Strategy     string `json:"strategy"`
		Force        bool   `json:"force"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	resolved, err := h.resolve(request, body.Workspace, body.SessionID, body.ConnectionID)
	if err != nil {
		return err
	}
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation:     targetfs.GitWorktreeMerge,
		Workspace:     resolved.Workspace,
		Branch:        strings.TrimSpace(body.SourceBranch),
		BaseBranch:    strings.TrimSpace(body.TargetBranch),
		MergeStrategy: strings.TrimSpace(body.Strategy),
		Force:         body.Force,
	})
	if err != nil {
		return err
	}
	var out targetfs.WorktreeMergeWire
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_merge", Detail: "无法解析 Worktree 合并结果", Err: err}
	}
	if out.Merged {
		h.publishWorktreesChanged(request, resolved, resolved.Workspace)
	}
	writeJSON(response, http.StatusOK, out)
	return nil
}

func (h *Handler) bootstrapWorktree(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Workspace    string   `json:"workspace"`
		SessionID    string   `json:"session_id"`
		ConnectionID string   `json:"connection_id"`
		TargetPath   string   `json:"target_path"`
		CopyFiles    []string `json:"copy_files"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	resolved, err := h.resolve(request, body.Workspace, body.SessionID, body.ConnectionID)
	if err != nil {
		return err
	}
	res, err := runGit(request.Context(), resolved, targetfs.GitRequest{
		Operation:  targetfs.GitWorktreeBootstrap,
		Workspace:  resolved.Workspace,
		TargetPath: strings.TrimSpace(body.TargetPath),
		CopyFiles:  body.CopyFiles,
	})
	if err != nil {
		return err
	}
	var out targetfs.WorktreeBootstrapWire
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return &targetfs.Error{Status: 502, Code: "invalid_worktree_bootstrap", Detail: "无法解析配置同步结果", Err: err}
	}
	writeJSON(response, http.StatusOK, out)
	return nil
}
