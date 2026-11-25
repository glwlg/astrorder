package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

const maxUploadBytes = 50 << 20 // 50 MiB

type fileMutator interface {
	Rename(context.Context, targetfs.RenameRequest) (targetfs.Info, error)
	Delete(context.Context, targetfs.DeleteRequest) error
}

func (h *Handler) rename(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Path         string `json:"path"`
		Name         string `json:"name"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	targetPath := targetfs.CleanPath(payload.Path)
	if targetPath == "" {
		return &targetfs.Error{Status: 400, Code: "path_required", Detail: "Path is required"}
	}
	resolved, err := h.resolve(request.Context(), fileParent(targetPath), payload.SessionID, payload.ConnectionID)
	if err != nil {
		return err
	}
	mutator, ok := resolved.FileSystem.(fileMutator)
	if !ok {
		return &targetfs.Error{Status: 501, Code: "rename_unsupported", Detail: "当前环境暂不支持重命名操作"}
	}
	info, err := mutator.Rename(request.Context(), targetfs.RenameRequest{
		Path: targetPath,
		Name: strings.TrimSpace(payload.Name),
	})
	if err != nil {
		return err
	}
	if err := h.publish(request.Context(), "files.changed", map[string]any{
		"action":        "rename",
		"old_path":      targetPath,
		"path":          info.Path,
		"connection_id": resolved.ConnectionID,
	}); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": info.Path, "name": info.Name})
	return nil
}

func (h *Handler) delete(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Path         string `json:"path"`
		Name         string `json:"name"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	targetPath := targetfs.CleanPath(payload.Path)
	if targetPath == "" {
		return &targetfs.Error{Status: 400, Code: "path_required", Detail: "Path is required"}
	}
	resolved, err := h.resolve(request.Context(), fileParent(targetPath), payload.SessionID, payload.ConnectionID)
	if err != nil {
		// If targetPath itself is a workspace root directory, parent lies outside allowed roots;
		// resolve targetPath directly so mutator.Delete can reject deleting the root with 400.
		if rootResolved, rootErr := h.resolve(request.Context(), targetPath, payload.SessionID, payload.ConnectionID); rootErr == nil {
			resolved = rootResolved
			err = nil
		}
	}
	if err != nil {
		return err
	}
	mutator, ok := resolved.FileSystem.(fileMutator)
	if !ok {
		return &targetfs.Error{Status: 501, Code: "delete_unsupported", Detail: "当前环境暂不支持删除操作"}
	}
	if err := mutator.Delete(request.Context(), targetfs.DeleteRequest{Path: targetPath}); err != nil {
		return err
	}
	if err := h.publish(request.Context(), "files.changed", map[string]any{
		"action":        "delete",
		"path":          targetPath,
		"connection_id": resolved.ConnectionID,
	}); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": targetPath})
	return nil
}

func sanitizeUploadFileName(raw string) (string, error) {
	base := strings.TrimSpace(filepath.Base(strings.ReplaceAll(raw, "\\", "/")))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "/\\\x00") {
		return "", &targetfs.Error{Status: 400, Code: "invalid_name", Detail: "上传文件名不合法"}
	}
	return base, nil
}

func (h *Handler) upload(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, maxUploadBytes+(2<<20))
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		return &targetfs.Error{Status: 400, Code: "invalid_multipart", Detail: "上传表单解析失败或文件超过 50 MB 限制", Err: err}
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}

	parent := targetfs.CleanPath(request.FormValue("parent"))
	if parent == "" {
		parent = targetfs.CleanPath(request.FormValue("dir"))
	}
	sessionID := strings.TrimSpace(request.FormValue("session_id"))
	connectionID := strings.TrimSpace(request.FormValue("connection_id"))
	overwrite := strings.EqualFold(strings.TrimSpace(request.FormValue("overwrite")), "true") || strings.TrimSpace(request.FormValue("overwrite")) == "1"

	if parent == "" && sessionID == "" {
		return &targetfs.Error{Status: 400, Code: "parent_required", Detail: "缺少目标目录参数"}
	}
	resolved, err := h.resolve(request.Context(), parent, sessionID, connectionID)
	if err != nil {
		return err
	}
	targetDir := parent
	if targetDir == "" {
		targetDir = resolved.Workspace
	}
	dirInfo, err := resolved.FileSystem.Stat(request.Context(), targetDir)
	if err != nil {
		return err
	}
	if !dirInfo.IsDir {
		targetDir = fileParent(dirInfo.Path)
	} else {
		targetDir = dirInfo.Path
	}

	file, header, err := request.FormFile("file")
	if err != nil {
		return &targetfs.Error{Status: 400, Code: "file_required", Detail: "请选择要上传的文件", Err: err}
	}
	defer file.Close()

	cleanName, err := sanitizeUploadFileName(header.Filename)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return &targetfs.Error{Status: 400, Code: "read_failed", Detail: "读取上传文件失败", Err: err}
	}
	if int64(len(data)) > maxUploadBytes {
		return &targetfs.Error{Status: 413, Code: "file_too_large", Detail: "单个文件不能超过 50 MB"}
	}

	destPath := filepath.Join(targetDir, cleanName)
	if !overwrite {
		ext := filepath.Ext(cleanName)
		stem := strings.TrimSuffix(cleanName, ext)
		for attempt := 1; attempt <= 50; attempt++ {
			if _, statErr := resolved.FileSystem.Stat(request.Context(), destPath); statErr != nil {
				break
			}
			destPath = filepath.Join(targetDir, fmt.Sprintf("%s (%d)%s", stem, attempt, ext))
		}
	}

	info, err := resolved.FileSystem.WriteAtomic(request.Context(), destPath, bytes.NewReader(data), 0o600)
	if err != nil {
		return err
	}
	if err := h.publish(request.Context(), "files.changed", map[string]any{
		"action":        "upload",
		"path":          info.Path,
		"connection_id": resolved.ConnectionID,
	}); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"ok":   true,
		"path": info.Path,
		"name": info.Name,
		"size": info.Size,
	})
	return nil
}
