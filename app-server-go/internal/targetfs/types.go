// Package targetfs provides scope-checked local, WSL, and SSH filesystem and
// Git operations. Remote targets execute the versioned astrorder-helper over a
// one-shot stdio protocol; this package never starts or proxies an Agent runtime.
package targetfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"time"
)

const ProtocolVersion = 1

// Error is a stable service error. Detail is safe to expose in the JSON
// {"detail": ...} HTTP error shape.
type Error struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Err    error  `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Detail
}

func (e *Error) Unwrap() error { return e.Err }

func errorf(status int, code, detail string, err error) *Error {
	return &Error{Status: status, Code: code, Detail: detail, Err: err}
}

func statusError(err error, fallback int, code, detail string) error {
	if err == nil {
		return nil
	}
	var target *Error
	if errors.As(err, &target) {
		return target
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errorf(fallback, code, detail, err)
}

// Info is portable file metadata. Path and Name preserve the target's exact
// spelling; modification time is UTC when available.
type Info struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	IsDir   bool        `json:"is_dir"`
	Size    int64       `json:"size,omitempty"`
	Mode    fs.FileMode `json:"mode,omitempty"`
	ModTime time.Time   `json:"mod_time,omitempty"`
}

// ReadResult keeps file content streaming. Callers must close Reader.
type ReadResult struct {
	Info        Info
	ContentType string
	Reader      io.ReadCloser
}

type Entry struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	IsDir    bool     `json:"is_dir"`
	Size     int64    `json:"size,omitempty"`
	Children *[]Entry `json:"children,omitempty"`
}

type ListRequest struct {
	Path       string `json:"path"`
	Depth      int    `json:"depth"`
	RevealPath string `json:"reveal_path,omitempty"`
}

type ListResult struct {
	Root   string   `json:"root"`
	Name   string   `json:"name"`
	Parent *string  `json:"parent"`
	Drives []string `json:"drives"`
	Items  []Entry  `json:"items"`
	// Truncated reports that deep directories were left unexpanded to keep the
	// listing bounded; their Children are unset and can be requested on demand.
	Truncated bool `json:"truncated,omitempty"`
}

type SearchRequest struct {
	Root  string `json:"root"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type SearchItem struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RelativePath string `json:"relative_path"`
}

type SearchResult struct {
	Items []SearchItem `json:"items"`
	Root  string       `json:"root"`
}

type MkdirRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

type StageRequest struct {
	Paths       []string `json:"paths"`
	Destination string   `json:"destination"`
}

type StageResult struct {
	Paths []string `json:"paths"`
}

type GitOperation string

const (
	GitBranches    GitOperation = "branches"
	GitStatus      GitOperation = "status"
	GitNumstat     GitOperation = "numstat"
	GitAheadBehind GitOperation = "ahead_behind"
	GitDiff        GitOperation = "diff"
	GitPathStatus  GitOperation = "path_status"
	GitCheckout    GitOperation = "checkout"
	// GitSnapshotReadonly is a typed, read-only composite operation: it returns
	// a canonical JSON WorkspaceSnapshot in GitResult.Stdout. Local targets run
	// it directly; remote targets run the identical operation inside the target
	// helper, so a remote workspace path is never resolved on the App Server.
	GitSnapshotReadonly GitOperation = "snapshot"
)

type GitRequest struct {
	Operation  GitOperation `json:"operation"`
	Workspace  string       `json:"workspace"`
	Path       string       `json:"path,omitempty"`
	BaseBranch string       `json:"base_branch,omitempty"`
	Branch     string       `json:"branch,omitempty"`
	Cached     bool         `json:"cached,omitempty"`
	Create     bool         `json:"create,omitempty"`
}

type GitResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
}

// FileSystem is the reusable target interface for F07 and later memory/profile
// consumers. Implementations must fail closed on scope or target resolution.
type FileSystem interface {
	Read(context.Context, string) (*ReadResult, error)
	WriteAtomic(context.Context, string, io.Reader, fs.FileMode) (Info, error)
	Stat(context.Context, string) (Info, error)
	List(context.Context, ListRequest) (ListResult, error)
	Mkdir(context.Context, MkdirRequest) (Info, error)
	Search(context.Context, SearchRequest) (SearchResult, error)
	Stage(context.Context, StageRequest) (StageResult, error)
	ExecGit(context.Context, GitRequest) (GitResult, error)
}

// Request and Response are the helper protocol envelopes. Request data is
// typed by Action; write payload bytes follow the JSON header on stdin.
type Request struct {
	Version     int             `json:"version"`
	Action      string          `json:"action"`
	Data        json.RawMessage `json:"data,omitempty"`
	PayloadSize int64           `json:"payload_size,omitempty"`
}

type Response struct {
	Version     int             `json:"version"`
	OK          bool            `json:"ok"`
	Data        json.RawMessage `json:"data,omitempty"`
	Error       *Error          `json:"error,omitempty"`
	PayloadSize int64           `json:"payload_size,omitempty"`
}
