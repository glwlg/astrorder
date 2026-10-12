// Command astrorder-memory-helper implements framed target filesystem
// operations (target-fs-v1) and strict home directory resolution over stdin/stdout.
// It is used on WSL and remote SSH targets by Astrorder memory sync without
// invoking shell interpreters.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/memory"
)

const maxTargetFrame = 20_000_000

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "target-fs-v1":
			if err := serveTargetFS(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "astrorder-memory-helper: %v\n", err)
				os.Exit(1)
			}
			return
		case "home":
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				fmt.Fprintf(os.Stderr, "astrorder-memory-helper: resolve home failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(filepath.ToSlash(home))
			return
		}
	}
	fmt.Fprintln(os.Stderr, "astrorder-memory-helper: command 'target-fs-v1' or 'home' is required")
	os.Exit(2)
}

func serveTargetFS(in io.Reader, out io.Writer) error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(in, lenBuf); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(lenBuf)
	if length > maxTargetFrame {
		return fmt.Errorf("frame size %d exceeds maximum limit %d", length, maxTargetFrame)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(in, payload); err != nil {
		return err
	}

	var req memory.TargetFileRequest
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return writeResponse(out, memory.TargetFileResponse{OK: false, Error: fmt.Sprintf("invalid request JSON: %v", err)})
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return writeResponse(out, memory.TargetFileResponse{OK: false, Error: "trailing JSON content rejected"})
	}

	cleanPath := filepath.Clean(req.Path)

	switch req.Operation {
	case memory.TargetFileRead:
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return writeResponse(out, memory.TargetFileResponse{OK: false, NotFound: true})
			}
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		return writeResponse(out, memory.TargetFileResponse{
			OK:         true,
			DataBase64: base64.StdEncoding.EncodeToString(data),
		})

	case memory.TargetFileWrite:
		data, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil {
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: "invalid base64"})
		}
		mode := os.FileMode(req.Mode)
		if mode == 0 {
			mode = 0o600
		}
		dir := filepath.Dir(cleanPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		tmpPattern := fmt.Sprintf(".%s.*.tmp", filepath.Base(cleanPath))
		tmpFile, err := os.CreateTemp(dir, tmpPattern)
		if err != nil {
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		tmpPath := tmpFile.Name()
		defer func() {
			_ = os.Remove(tmpPath)
		}()
		if err := tmpFile.Chmod(mode); err != nil {
			_ = tmpFile.Close()
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		if _, err := tmpFile.Write(data); err != nil {
			_ = tmpFile.Close()
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		if err := tmpFile.Sync(); err != nil {
			_ = tmpFile.Close()
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		if err := tmpFile.Close(); err != nil {
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		if err := atomicReplace(tmpPath, cleanPath); err != nil {
			return writeResponse(out, memory.TargetFileResponse{OK: false, Error: err.Error()})
		}
		return writeResponse(out, memory.TargetFileResponse{OK: true})

	default:
		return writeResponse(out, memory.TargetFileResponse{OK: false, Error: "unsupported operation"})
	}
}

func writeResponse(out io.Writer, resp memory.TargetFileResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	frame, err := memory.EncodeFrame(raw)
	if err != nil {
		return err
	}
	_, err = out.Write(frame)
	return err
}

func atomicReplace(source, destination string) error {
	err := os.Rename(source, destination)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists") {
		_ = os.Remove(destination)
		return os.Rename(source, destination)
	}
	return err
}
