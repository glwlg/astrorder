package targetfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SSHConfig struct {
	Binary       string
	Host         string
	Alias        string
	Port         int
	User         string
	IdentityFile string
	ConfigFile   string
	HelperPath   string
}

type WSLConfig struct {
	Binary     string
	Distro     string
	HelperPath string
}

type RemoteOptions struct {
	Workspace    string
	AllowedRoots []string
	Command      []string
	Environment  []string
	StageRoots   []string
}

type remoteFileSystem struct {
	workspace    string
	allowedRoots []string
	command      []string
	environment  []string
	stageRoots   []string
}

var safeSSHName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var safeSSHUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var safeWSLDistro = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func BuildSSHCommand(config SSHConfig) ([]string, error) {
	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = "ssh"
	}
	if strings.HasPrefix(binary, "-") || strings.ContainsAny(binary, "\x00\r\n") {
		return nil, errorf(422, "invalid_ssh_binary", "SSH executable is invalid", nil)
	}
	target := strings.TrimSpace(config.Alias)
	if target == "" {
		target = strings.TrimSpace(config.Host)
	}
	if !safeSSHName.MatchString(target) || strings.HasPrefix(target, "-") {
		return nil, errorf(422, "invalid_ssh_target", "SSH target is invalid", nil)
	}
	if config.User != "" && !safeSSHUser.MatchString(config.User) {
		return nil, errorf(422, "invalid_ssh_user", "SSH user is invalid", nil)
	}
	if config.Port < 0 || config.Port > 65535 {
		return nil, errorf(422, "invalid_ssh_port", "SSH port must be between 1 and 65535", nil)
	}
	if config.IdentityFile != "" && (strings.HasPrefix(config.IdentityFile, "-") || strings.ContainsAny(config.IdentityFile, "\x00\r\n")) {
		return nil, errorf(422, "invalid_identity_file", "SSH identity path is invalid", nil)
	}
	if config.ConfigFile != "" && (strings.HasPrefix(config.ConfigFile, "-") || strings.ContainsAny(config.ConfigFile, "\x00\r\n")) {
		return nil, errorf(422, "invalid_ssh_config", "SSH config path is invalid", nil)
	}
	helper := strings.TrimSpace(config.HelperPath)
	if helper == "" || strings.ContainsRune(helper, 0) {
		return nil, errorf(503, "helper_not_configured", "Remote Astrorder helper is not configured", nil)
	}
	argv := []string{binary, "-o", "BatchMode=yes"}
	if config.ConfigFile != "" {
		argv = append(argv, "-F", filepath.Clean(config.ConfigFile))
	}
	if config.Port > 0 {
		argv = append(argv, "-p", strconv.Itoa(config.Port))
	}
	if config.User != "" {
		argv = append(argv, "-l", config.User)
	}
	if config.IdentityFile != "" {
		argv = append(argv, "-i", filepath.Clean(config.IdentityFile))
	}
	remoteCommand := "exec " + quotePOSIX(helper) + " '--stdio' '--protocol' '" + strconv.Itoa(ProtocolVersion) + "'"
	return append(argv, "--", target, remoteCommand), nil
}

func BuildWSLCommand(config WSLConfig) ([]string, error) {
	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = "wsl.exe"
	}
	if strings.HasPrefix(binary, "-") || strings.ContainsAny(binary, "\x00\r\n") {
		return nil, errorf(422, "invalid_wsl_binary", "WSL executable is invalid", nil)
	}
	if !safeWSLDistro.MatchString(config.Distro) || strings.HasPrefix(config.Distro, "-") {
		return nil, errorf(422, "invalid_wsl_distro", "WSL distribution is invalid", nil)
	}
	helper := strings.TrimSpace(config.HelperPath)
	if helper == "" || strings.ContainsRune(helper, 0) {
		return nil, errorf(503, "helper_not_configured", "WSL Astrorder helper is not configured", nil)
	}
	return []string{binary, "-d", config.Distro, "--", helper, "--stdio", "--protocol", strconv.Itoa(ProtocolVersion)}, nil
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func NewRemote(options RemoteOptions) (FileSystem, error) {
	if strings.TrimSpace(options.Workspace) == "" {
		return nil, errorf(422, "workspace_required", "Workspace is required", nil)
	}
	if len(options.Command) == 0 || strings.TrimSpace(options.Command[0]) == "" {
		return nil, errorf(503, "helper_command_required", "Target helper command is required", nil)
	}
	roots := append([]string(nil), options.AllowedRoots...)
	if len(roots) == 0 {
		roots = []string{options.Workspace}
	}
	return &remoteFileSystem{
		workspace: options.Workspace, allowedRoots: roots,
		command: append([]string(nil), options.Command...), environment: append([]string(nil), options.Environment...),
		stageRoots: append([]string(nil), options.StageRoots...),
	}, nil
}

type scopeRequest struct {
	Workspace    string          `json:"workspace"`
	AllowedRoots []string        `json:"allowed_roots"`
	Request      json.RawMessage `json:"request"`
}

func (r *remoteFileSystem) envelope(value any) (json.RawMessage, error) {
	request, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(scopeRequest{Workspace: r.workspace, AllowedRoots: r.allowedRoots, Request: request})
}

func mapInfoPath(info Info, path string) Info {
	info.Path = path
	return info
}

func (r *remoteFileSystem) Read(ctx context.Context, path string) (*ReadResult, error) {
	data, err := r.envelope(struct {
		Path string `json:"path"`
	}{Path: path})
	if err != nil {
		return nil, err
	}
	response, reader, err := r.start(ctx, Request{Version: ProtocolVersion, Action: "read", Data: data})
	if err != nil {
		return nil, err
	}
	var metadata struct {
		Info        Info   `json:"info"`
		ContentType string `json:"content_type"`
	}
	if err := json.Unmarshal(response.Data, &metadata); err != nil {
		reader.Close()
		return nil, errorf(502, "invalid_helper_response", "Target helper returned invalid file metadata", err)
	}
	return &ReadResult{Info: metadata.Info, ContentType: metadata.ContentType, Reader: reader}, nil
}

func (r *remoteFileSystem) WriteAtomic(ctx context.Context, path string, source io.Reader, mode fs.FileMode) (Info, error) {
	data, err := r.envelope(struct {
		Path      string      `json:"path"`
		Mode      fs.FileMode `json:"mode"`
		Streaming bool        `json:"streaming"`
	}{Path: path, Mode: mode, Streaming: true})
	if err != nil {
		return Info{}, err
	}
	response, err := r.invoke(ctx, Request{Version: ProtocolVersion, Action: "write_atomic", Data: data, PayloadSize: -1}, source)
	if err != nil {
		return Info{}, err
	}
	var result Info
	if err := json.Unmarshal(response.Data, &result); err != nil {
		return Info{}, errorf(502, "invalid_helper_response", "Target helper returned invalid write metadata", err)
	}
	return result, nil
}

func (r *remoteFileSystem) Stat(ctx context.Context, path string) (Info, error) {
	var result Info
	err := r.invokeJSON(ctx, "stat", struct {
		Path string `json:"path"`
	}{Path: path}, &result)
	return result, err
}

func (r *remoteFileSystem) List(ctx context.Context, request ListRequest) (ListResult, error) {
	var result ListResult
	err := r.invokeJSON(ctx, "list", request, &result)
	return result, err
}

func (r *remoteFileSystem) Mkdir(ctx context.Context, request MkdirRequest) (Info, error) {
	var result Info
	err := r.invokeJSON(ctx, "mkdir", request, &result)
	return result, err
}

func (r *remoteFileSystem) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	var result SearchResult
	err := r.invokeJSON(ctx, "search", request, &result)
	return result, err
}

func (r *remoteFileSystem) Stage(ctx context.Context, request StageRequest) (StageResult, error) {
	if len(r.stageRoots) == 0 {
		return StageResult{}, errorf(503, "stage_root_not_configured", "Local staging storage is not configured", nil)
	}
	stageRoot, err := canonicalStageRoot(request.Destination, r.stageRoots)
	if err != nil {
		return StageResult{}, err
	}
	data, err := r.envelope(struct {
		Paths []string `json:"paths"`
	}{Paths: request.Paths})
	if err != nil {
		return StageResult{}, err
	}
	batch, err := os.MkdirTemp(stageRoot, "copy-*")
	if err != nil {
		return StageResult{}, errorf(500, "stage_failed", "Failed to prepare staging batch", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(batch)
		}
	}()
	_, reader, err := r.start(ctx, Request{Version: ProtocolVersion, Action: "export", Data: data})
	if err != nil {
		return StageResult{}, err
	}
	defer reader.Close()
	result, err := extractExport(ctx, reader, batch)
	if err != nil {
		return StageResult{}, err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return StageResult{}, err
	}
	if err := reader.Close(); err != nil {
		return StageResult{}, err
	}
	completed = true
	return result, nil
}

func (r *remoteFileSystem) ExecGit(ctx context.Context, request GitRequest) (GitResult, error) {
	var result GitResult
	err := r.invokeJSON(ctx, "git", request, &result)
	return result, err
}

func (r *remoteFileSystem) invokeJSON(ctx context.Context, action string, value, result any) error {
	data, err := r.envelope(value)
	if err != nil {
		return err
	}
	response, err := r.invoke(ctx, Request{Version: ProtocolVersion, Action: action, Data: data}, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response.Data, result); err != nil {
		return errorf(502, "invalid_helper_response", "Target helper returned invalid JSON", err)
	}
	return nil
}

func (r *remoteFileSystem) invoke(ctx context.Context, request Request, payload io.Reader) (Response, error) {
	response, reader, err := r.startWithPayload(ctx, request, payload)
	if err != nil {
		return Response{}, err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		reader.Close()
		return Response{}, errorf(502, "helper_payload_failed", "Target helper response failed", err)
	}
	if err := reader.Close(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func (r *remoteFileSystem) start(ctx context.Context, request Request) (Response, io.ReadCloser, error) {
	return r.startWithPayload(ctx, request, nil)
}

// remoteSlots bounds how many helper processes may be starting against one
// target at once, and retryableTransport reports a failure that happens before
// the helper answers. Every file operation opens its own SSH session, and sshd
// refuses the connections that arrive faster than MaxStartups allows
// ("kex_exchange_identification"), which would surface as a 502 in the file
// panel even though the target is fine.
const (
	remoteStartSlots   = 4
	remoteStartRetries = 3
)

var remoteSlots sync.Map // key: joined command -> chan struct{}

func (r *remoteFileSystem) slot() chan struct{} {
	key := strings.Join(r.command, " ")
	value, _ := remoteSlots.LoadOrStore(key, make(chan struct{}, remoteStartSlots))
	return value.(chan struct{})
}

func retryableTransport(err error) bool {
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "helper_transport_failed" {
		return false
	}
	text := failure.Detail
	return strings.Contains(text, "kex_exchange_identification") || strings.Contains(text, "Connection reset") ||
		strings.Contains(text, "Connection closed by") || strings.Contains(text, "Connection timed out during banner")
}

func (r *remoteFileSystem) startWithPayload(ctx context.Context, request Request, payload io.Reader) (Response, io.ReadCloser, error) {
	// A streamed payload can be read only once, so only payload-free requests retry.
	attempts := 1
	if payload == nil {
		attempts = remoteStartRetries
	}
	slot := r.slot()
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return Response{}, nil, ctx.Err()
		}
		response, reader, err := r.startOnce(ctx, request, payload)
		<-slot
		if err == nil || !retryableTransport(err) {
			return response, reader, err
		}
		last = err
		select {
		case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
		case <-ctx.Done():
			return Response{}, nil, ctx.Err()
		}
	}
	return Response{}, nil, last
}

func (r *remoteFileSystem) startOnce(ctx context.Context, request Request, payload io.Reader) (Response, io.ReadCloser, error) {
	command := exec.CommandContext(ctx, r.command[0], r.command[1:]...)
	if len(r.environment) > 0 {
		command.Env = append(os.Environ(), r.environment...)
	}
	configureChildProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return Response{}, nil, errorf(503, "helper_start_failed", "Unable to start target helper", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Response{}, nil, errorf(503, "helper_start_failed", "Unable to start target helper", err)
	}
	var stderr synchronizedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return Response{}, nil, errorf(503, "helper_start_failed", "Unable to start target helper", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Response{}, nil, err
	}
	writeResult := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write(append(encoded, '\n'))
		if writeErr == nil && payload != nil {
			_, writeErr = copyContext(ctx, stdin, payload)
		}
		closeErr := stdin.Close()
		writeResult <- errors.Join(writeErr, closeErr)
	}()
	buffered := bufio.NewReaderSize(stdout, 64*1024)
	line, err := readJSONLine(buffered, maxHelperResponseHeader)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Response{}, nil, helperTransportError(err, stderr.String())
	}
	if err := <-writeResult; err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Response{}, nil, statusError(err, 502, "helper_request_failed", "Target helper request failed")
	}
	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Response{}, nil, errorf(502, "invalid_helper_response", "Target helper returned an invalid response", err)
	}
	if response.Version != ProtocolVersion {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Response{}, nil, errorf(502, "helper_version_mismatch", "Target helper protocol version does not match", nil)
	}
	if !response.OK {
		_ = command.Process.Kill()
		_ = command.Wait()
		if response.Error == nil {
			return Response{}, nil, errorf(502, "helper_failed", "Target helper failed without error detail", nil)
		}
		return Response{}, nil, response.Error
	}
	reader := &processReadCloser{
		reader: buffered, command: command, stderr: &stderr, remaining: response.PayloadSize,
	}
	return response, reader, nil
}

// maxHelperResponseHeader bounds one JSON response line from the helper. A
// workspace listing is one line, so this must be well above 1 MiB.
const maxHelperResponseHeader = 16 * 1024 * 1024

func helperTransportError(err error, stderr string) error {
	detail := "Target helper transport failed"
	clean := strings.TrimSpace(stderr)
	if clean != "" {
		if len(clean) > 1000 {
			clean = clean[len(clean)-1000:]
		}
		detail += ": " + clean
	}
	return errorf(502, "helper_transport_failed", detail, err)
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type processReadCloser struct {
	reader    *bufio.Reader
	command   *exec.Cmd
	stderr    *synchronizedBuffer
	remaining int64
	complete  bool
	closed    bool
}

func (r *processReadCloser) finish() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.wait()
}

func (r *processReadCloser) wait() error {
	err := r.command.Wait()
	if err != nil {
		return helperTransportError(err, r.stderr.String())
	}
	return nil
}

func (r *processReadCloser) Read(buffer []byte) (int, error) {
	if r.closed {
		return 0, fs.ErrClosed
	}
	if r.remaining == 0 {
		r.complete = true
		if err := r.finish(); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if r.remaining > 0 && int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	length, err := r.reader.Read(buffer)
	if r.remaining > 0 {
		r.remaining -= int64(length)
		if r.remaining == 0 {
			r.complete = true
			if finishErr := r.wait(); finishErr != nil && err == nil {
				err = finishErr
			} else if err == nil {
				err = io.EOF
			}
			r.closed = true
		}
	}
	if errors.Is(err, io.EOF) {
		r.complete = r.remaining <= 0
		if r.complete && !r.closed {
			if finishErr := r.finish(); finishErr != nil {
				return length, finishErr
			}
		}
	}
	return length, err
}

func (r *processReadCloser) Close() error {
	if r.closed {
		return nil
	}
	if !r.complete && r.command.Process != nil {
		_ = r.command.Process.Kill()
	}
	return r.finish()
}

func readJSONLine(reader *bufio.Reader, maximum int) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > maximum {
		return nil, errors.New("JSON header exceeds limit")
	}
	return bytes.TrimSuffix(line, []byte{'\n'}), nil
}

func canonicalStageRoot(requested string, allowed []string) (string, error) {
	if requested == "" {
		return "", errorf(400, "stage_destination_required", "Staging destination is required", nil)
	}
	candidate, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		return "", errorf(400, "invalid_stage_destination", "Staging destination is invalid", err)
	}
	for _, raw := range allowed {
		root, err := filepath.Abs(filepath.Clean(raw))
		if err == nil && within(candidate, root) {
			if err := os.MkdirAll(candidate, 0o700); err != nil {
				return "", errorf(500, "stage_failed", "Failed to create staging directory", err)
			}
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", errorf(500, "stage_failed", "Failed to inspect staging directory", err)
			}
			resolvedRoot, err := filepath.EvalSymlinks(root)
			if err != nil || !within(resolved, resolvedRoot) {
				return "", errorf(403, "stage_out_of_scope", "Staging destination is outside allowed roots", err)
			}
			return resolved, nil
		}
	}
	return "", errorf(403, "stage_out_of_scope", "Staging destination is outside allowed roots", nil)
}

var _ FileSystem = (*remoteFileSystem)(nil)
var _ = fmt.Sprintf
