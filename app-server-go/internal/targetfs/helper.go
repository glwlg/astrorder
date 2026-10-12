package targetfs

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const maxHelperRequestHeader = 1024 * 1024

// ServeHelper processes one protocol request. It is exported for the
// astrorder-helper command and black-box protocol tests.
func ServeHelper(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReaderSize(input, 128*1024)
	line, err := readJSONLine(reader, maxHelperRequestHeader)
	if err != nil {
		return writeHelperError(output, errorf(400, "invalid_request", "Helper request header is invalid", err))
	}
	var request Request
	if err := json.Unmarshal(line, &request); err != nil {
		return writeHelperError(output, errorf(400, "invalid_request", "Helper request JSON is invalid", err))
	}
	if request.Version != ProtocolVersion {
		return writeHelperError(output, errorf(400, "protocol_version_mismatch", "Helper protocol version does not match", nil))
	}
	var scope scopeRequest
	if err := json.Unmarshal(request.Data, &scope); err != nil {
		return writeHelperError(output, errorf(400, "invalid_scope", "Helper scope is invalid", err))
	}
	filesystem, err := NewLocal(LocalOptions{Workspace: scope.Workspace, AllowedRoots: scope.AllowedRoots})
	if err != nil {
		return writeHelperError(output, err)
	}

	switch request.Action {
	case "read":
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Read request is invalid", err))
		}
		result, err := filesystem.Read(ctx, payload.Path)
		if err != nil {
			return writeHelperError(output, err)
		}
		defer result.Reader.Close()
		metadata := struct {
			Info        Info   `json:"info"`
			ContentType string `json:"content_type"`
		}{Info: result.Info, ContentType: result.ContentType}
		return writeHelperStream(output, metadata, result.Info.Size, result.Reader)
	case "write_atomic":
		var payload struct {
			Path      string      `json:"path"`
			Mode      fs.FileMode `json:"mode"`
			Streaming bool        `json:"streaming"`
		}
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Write request is invalid", err))
		}
		if request.PayloadSize < 0 && !payload.Streaming {
			return writeHelperError(output, errorf(400, "invalid_request", "Write request streaming mode is invalid", nil))
		}
		var source io.Reader = reader
		if request.PayloadSize >= 0 {
			source = &exactReader{source: reader, remaining: request.PayloadSize}
		}
		result, err := filesystem.WriteAtomic(ctx, payload.Path, source, payload.Mode)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "stat":
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Stat request is invalid", err))
		}
		result, err := filesystem.Stat(ctx, payload.Path)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "list":
		var payload ListRequest
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "List request is invalid", err))
		}
		result, err := filesystem.List(ctx, payload)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "mkdir":
		var payload MkdirRequest
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Mkdir request is invalid", err))
		}
		result, err := filesystem.Mkdir(ctx, payload)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "search":
		var payload SearchRequest
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Search request is invalid", err))
		}
		result, err := filesystem.Search(ctx, payload)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "git":
		var payload GitRequest
		if err := json.Unmarshal(scope.Request, &payload); err != nil {
			return writeHelperError(output, errorf(400, "invalid_request", "Git request is invalid", err))
		}
		result, err := filesystem.ExecGit(ctx, payload)
		if err != nil {
			return writeHelperError(output, err)
		}
		return writeHelperJSON(output, result)
	case "export":
		var payload struct {
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal(scope.Request, &payload); err != nil || len(payload.Paths) == 0 {
			return writeHelperError(output, errorf(400, "invalid_request", "Export request is invalid", err))
		}
		local, ok := filesystem.(*localFileSystem)
		if !ok {
			return writeHelperError(output, errorf(500, "export_failed", "Target helper export is unavailable", nil))
		}
		paths := make([]string, len(payload.Paths))
		for index, raw := range payload.Paths {
			path, pathErr := local.existingPath(raw)
			if pathErr != nil {
				return writeHelperError(output, pathErr)
			}
			paths[index] = path
		}
		if err := validateExportPaths(ctx, paths, local.roots); err != nil {
			return writeHelperError(output, err)
		}
		if err := writeHelperHeader(output, Response{Version: ProtocolVersion, OK: true, PayloadSize: -1}); err != nil {
			return err
		}
		return writeExport(ctx, output, paths)
	default:
		return writeHelperError(output, errorf(400, "unsupported_action", "Helper action is not supported", nil))
	}
}

type exactReader struct {
	source    io.Reader
	remaining int64
}

func (r *exactReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	length, err := r.source.Read(buffer)
	r.remaining -= int64(length)
	if errors.Is(err, io.EOF) && r.remaining > 0 {
		return length, io.ErrUnexpectedEOF
	}
	return length, err
}

func writeHelperJSON(output io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeHelperHeader(output, Response{Version: ProtocolVersion, OK: true, Data: data})
}

func writeHelperStream(output io.Writer, metadata any, size int64, source io.Reader) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := writeHelperHeader(output, Response{Version: ProtocolVersion, OK: true, Data: data, PayloadSize: size}); err != nil {
		return err
	}
	written, err := io.Copy(output, source)
	if err != nil {
		return err
	}
	if written != size {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func writeHelperError(output io.Writer, err error) error {
	var target *Error
	if !errors.As(err, &target) {
		target = errorf(500, "internal_error", "Target helper failed", err)
	}
	return writeHelperHeader(output, Response{Version: ProtocolVersion, OK: false, Error: target})
}

func writeHelperHeader(output io.Writer, response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = output.Write(append(encoded, '\n'))
	return err
}

func validateExportPaths(ctx context.Context, paths, roots []string) error {
	seen := make(map[string]bool)
	for _, source := range paths {
		name := filepath.Base(source)
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return errorf(409, "stage_name_conflict", "Staged paths have duplicate filenames", nil)
		}
		seen[key] = true
		err := filepath.WalkDir(source, func(path string, _ fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if !withinAny(canonical, roots) {
				return errorf(403, "path_out_of_scope", "Staged directory contains a path outside allowed workspaces", nil)
			}
			return nil
		})
		if err != nil {
			return statusError(err, 500, "export_failed", "Failed to validate exported files")
		}
	}
	return nil
}

func writeExport(ctx context.Context, output io.Writer, paths []string) error {
	sorted := append([]string(nil), paths...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := strings.ToLower(filepath.Base(sorted[i])), strings.ToLower(filepath.Base(sorted[j]))
		if left == right {
			return sorted[i] < sorted[j]
		}
		return left < right
	})
	for _, source := range sorted {
		if err := writeExportTree(ctx, output, source, filepath.Base(source)); err != nil {
			return err
		}
	}
	return binary.Write(output, binary.BigEndian, uint32(0))
}

func writeExportTree(ctx context.Context, output io.Writer, source, relative string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	entry := exportEntry{Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm()), Size: info.Size()}
	if info.IsDir() {
		entry.Type = "directory"
		entry.Size = 0
	} else if info.Mode().IsRegular() {
		entry.Type = "file"
	} else {
		return errorf(422, "unsupported_file_type", "Only regular files and directories can be staged", nil)
	}
	header, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(header) > maxExportHeader {
		return errorf(500, "export_failed", "Export entry metadata is too large", nil)
	}
	if err := binary.Write(output, binary.BigEndian, uint32(len(header))); err != nil {
		return err
	}
	if _, err := output.Write(header); err != nil {
		return err
	}
	if entry.Type == "file" {
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		written, copyErr := copyContext(ctx, output, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if written != info.Size() {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	children, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	sort.SliceStable(children, func(i, j int) bool {
		left, right := strings.ToLower(children[i].Name()), strings.ToLower(children[j].Name())
		if left == right {
			return children[i].Name() < children[j].Name()
		}
		return left < right
	})
	for _, child := range children {
		if err := writeExportTree(ctx, output, filepath.Join(source, child.Name()), filepath.Join(relative, child.Name())); err != nil {
			return err
		}
	}
	return nil
}

func contentType(path string, source io.ReadSeeker) (string, error) {
	value := mime.TypeByExtension(filepath.Ext(path))
	if value != "" {
		return value, nil
	}
	probe := make([]byte, 512)
	length, err := source.Read(probe)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(probe[:length]), nil
}

var _ = contentType
