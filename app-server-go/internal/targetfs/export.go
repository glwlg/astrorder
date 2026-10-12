package targetfs

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxExportHeader = 1024 * 1024

type exportEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Mode uint32 `json:"mode"`
	Size int64  `json:"size"`
}

func extractExport(ctx context.Context, source io.Reader, destination string) (StageResult, error) {
	reader := bufio.NewReaderSize(source, 128*1024)
	created := make([]string, 0)
	roots := make([]string, 0)
	seenRoots := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			cleanupExport(created)
			return StageResult{}, err
		}
		var length uint32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			cleanupExport(created)
			if errors.Is(err, io.EOF) {
				return StageResult{}, errorf(502, "invalid_export", "Remote export ended without a terminator", err)
			}
			return StageResult{}, errorf(502, "invalid_export", "Remote export framing is invalid", err)
		}
		if length == 0 {
			return StageResult{Paths: roots}, nil
		}
		if length > maxExportHeader {
			cleanupExport(created)
			return StageResult{}, errorf(502, "invalid_export", "Remote export entry is too large", nil)
		}
		header := make([]byte, length)
		if _, err := io.ReadFull(reader, header); err != nil {
			cleanupExport(created)
			return StageResult{}, errorf(502, "invalid_export", "Remote export entry is incomplete", err)
		}
		var entry exportEntry
		if err := json.Unmarshal(header, &entry); err != nil {
			cleanupExport(created)
			return StageResult{}, errorf(502, "invalid_export", "Remote export entry is invalid", err)
		}
		relative, err := safeExportPath(entry.Path)
		if err != nil {
			cleanupExport(created)
			return StageResult{}, err
		}
		target := filepath.Join(destination, relative)
		if !within(target, destination) {
			cleanupExport(created)
			return StageResult{}, errorf(502, "invalid_export_path", "Remote export path escapes staging storage", nil)
		}
		rootName := strings.Split(filepath.ToSlash(relative), "/")[0]
		rootPath := filepath.Join(destination, filepath.FromSlash(rootName))
		rootKey := rootPath
		if runtime.GOOS == "windows" {
			rootKey = strings.ToLower(rootKey)
		}
		if !seenRoots[rootKey] {
			if _, err := os.Lstat(rootPath); err == nil {
				cleanupExport(created)
				return StageResult{}, errorf(409, "stage_target_exists", "A staged target already exists", nil)
			} else if !os.IsNotExist(err) {
				cleanupExport(created)
				return StageResult{}, errorf(500, "stage_failed", "Failed to inspect staging target", err)
			}
			seenRoots[rootKey] = true
			roots = append(roots, rootPath)
		}
		switch entry.Type {
		case "directory":
			if entry.Size != 0 {
				cleanupExport(created)
				return StageResult{}, errorf(502, "invalid_export", "Remote directory export has a payload", nil)
			}
			if err := os.MkdirAll(target, os.FileMode(entry.Mode).Perm()); err != nil {
				cleanupExport(created)
				return StageResult{}, errorf(500, "stage_failed", "Failed to create staged directory", err)
			}
			created = append(created, target)
		case "file":
			if entry.Size < 0 {
				cleanupExport(created)
				return StageResult{}, errorf(502, "invalid_export", "Remote file export has an invalid size", nil)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				cleanupExport(created)
				return StageResult{}, errorf(500, "stage_failed", "Failed to create staged directory", err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(entry.Mode).Perm())
			if err != nil {
				cleanupExport(created)
				return StageResult{}, errorf(500, "stage_failed", "Failed to create staged file", err)
			}
			created = append(created, target)
			_, copyErr := copyContext(ctx, file, io.LimitReader(reader, entry.Size))
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				cleanupExport(created)
				return StageResult{}, statusError(errors.Join(copyErr, closeErr), 502, "invalid_export", "Remote file export is incomplete")
			}
		default:
			cleanupExport(created)
			return StageResult{}, errorf(502, "invalid_export", "Remote export contains an unsupported entry", nil)
		}
	}
}

func safeExportPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return "", errorf(502, "invalid_export_path", "Remote export path is invalid", nil)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || filepath.IsAbs(filepath.FromSlash(value)) {
		return "", errorf(502, "invalid_export_path", "Remote export path is invalid", nil)
	}
	return filepath.FromSlash(clean), nil
}

func cleanupExport(paths []string) {
	for index := len(paths) - 1; index >= 0; index-- {
		_ = os.RemoveAll(paths[index])
	}
}
