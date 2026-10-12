// Package envtarget gives the settings features (capabilities, skills, RTK) one
// way to read files, write files and run programs on a machine Astrorder
// manages: the local host, a WSL distro, or an SSH connection. Everything on a
// remote target is plain POSIX sh, so no Python or helper binary is needed
// there.
package envtarget

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/targetfs"
)

// Target is how a machine is listed to the browser.
type Target struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // local | wsl | ssh
	Name         string `json:"name"`
	ConnectionID string `json:"connection_id,omitempty"`
	Distro       string `json:"distro,omitempty"`
	State        string `json:"state,omitempty"`
	Profile      string `json:"-"` // Hermes profile of an SSH connection
}

// Paths are the agent homes on a target, always slash-separated.
type Paths struct {
	Home   string
	Codex  string
	Hermes string
	Grok   string
	Agents string // shared ~/.agents used by the skills CLI
}

type Command struct {
	Name  string
	Args  []string
	Env   map[string]string
	Stdin []byte
}

type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Output is stdout, or stderr when stdout is empty, trimmed.
func (r Result) Output() string {
	if text := strings.TrimSpace(r.Stdout); text != "" {
		return text
	}
	return strings.TrimSpace(r.Stderr)
}

// Error carries the HTTP status a failure should be reported with.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string { return e.Detail }

func Errorf(status int, format string, args ...any) *Error {
	return &Error{Status: status, Detail: fmt.Sprintf(format, args...)}
}

var ErrNotExist = fs.ErrNotExist

// Env is one machine.
type Env interface {
	Target() Target
	IsLocal() bool
	Paths(ctx context.Context) (Paths, error)
	Read(ctx context.Context, name string) ([]byte, error)
	Write(ctx context.Context, name string, data []byte, mode fs.FileMode) error
	Remove(ctx context.Context, name string) error
	Run(ctx context.Context, command Command) (Result, error)
	// Shell runs a POSIX sh script. It exists on remote targets only.
	Shell(ctx context.Context, script string, stdin []byte, args ...string) (Result, error)
	// Which locates an executable the way Run would.
	Which(ctx context.Context, name string) (string, bool)
	// ReadMany reads several files in one round trip; missing files are
	// absent from the result.
	ReadMany(ctx context.Context, names []string) (map[string][]byte, error)
	// RemoveAll deletes a file or directory tree.
	RemoveAll(ctx context.Context, name string) error
	IsDir(ctx context.Context, name string) bool
	// Rename moves a file or directory, creating the destination's parent.
	Rename(ctx context.Context, from, to string) error
	// ListDir lists the entries of one directory (missing directory: none).
	// PackDir returns dir (its last element is the archive's top level) as a
	// tar.gz; UnpackDir replaces parent/name with the archive's directory.
	PackDir(ctx context.Context, dir string) ([]byte, error)
	UnpackDir(ctx context.Context, parent, name string, data []byte) error
	ListDir(ctx context.Context, dir string) ([]DirEntry, error)
	// Find walks roots (symlinks followed, at most depth levels) for files
	// called filename and returns each with its first head lines.
	Find(ctx context.Context, roots []string, filename string, depth, head int) ([]Found, error)
}

type DirEntry struct {
	Name string
	Dir  bool
}

type Found struct {
	Path string
	Head string
}

// ---- resolver ---------------------------------------------------------

type ConnectionStore interface {
	ListSSHConnections(context.Context) ([]map[string]any, error)
}

type RemoteShell interface {
	Shell(ctx context.Context, connectionID string, input []byte, script string, args ...string) (targetfs.ShellResult, error)
}

type Resolver struct {
	Store     ConnectionStore
	Remote    RemoteShell
	WSLBinary string

	pathCache sync.Map // target id -> Paths
}

func str(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}

func looksLikeWSL(connection map[string]any) bool {
	settings, _ := connection["settings"].(map[string]any)
	host := strings.ToLower(str(settings, "host"))
	label := strings.ToLower(str(connection, "display_name") + " " + str(connection, "profile_name"))
	return host == "127.0.0.1" || host == "localhost" || strings.Contains(label, "wsl")
}

// List returns the local host, WSL distros that have no SSH connection of
// their own, and every SSH connection.
func (r *Resolver) List(ctx context.Context) ([]Target, error) {
	targets := []Target{{ID: "local", Kind: "local", Name: "本机"}}
	var connections []map[string]any
	if r.Store != nil {
		var err error
		if connections, err = r.Store.ListSSHConnections(ctx); err != nil {
			return nil, Errorf(500, "读取 SSH 连接失败")
		}
	}
	hasWSLConnection := false
	for _, connection := range connections {
		hasWSLConnection = hasWSLConnection || looksLikeWSL(connection)
	}
	if runtime.GOOS == "windows" && !hasWSLConnection {
		binary := r.WSLBinary
		if binary == "" {
			binary = "wsl.exe"
		}
		if out, err := hiddenCommand(ctx, binary, "-l", "-q").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				distro := strings.TrimSpace(strings.ReplaceAll(line, "\x00", ""))
				if distro != "" {
					targets = append(targets, Target{ID: "wsl:" + distro, Kind: "wsl", Name: "WSL · " + distro, Distro: distro})
				}
			}
		}
	}
	for _, connection := range connections {
		id := str(connection, "id")
		if id == "" {
			continue
		}
		name := str(connection, "display_name")
		if name == "" {
			name = str(connection, "profile_name")
		}
		if name == "" {
			name = id
		}
		targets = append(targets, Target{ID: "ssh:" + id, Kind: "ssh", Name: name, ConnectionID: id, State: str(connection, "state"), Profile: str(connection, "profile_name")})
	}
	return targets, nil
}

// Open returns the Env for a target id from List.
func (r *Resolver) Open(ctx context.Context, id string) (Env, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "local" {
		return &localEnv{target: Target{ID: "local", Kind: "local", Name: "本机"}}, nil
	}
	targets, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target.ID != id && (target.ConnectionID == "" || target.ConnectionID != id) {
			continue
		}
		if r.Remote == nil {
			return nil, Errorf(503, "远程目标不可用")
		}
		connection := target.ConnectionID
		if target.Kind == "wsl" {
			connection = "wsl:" + target.Distro
		}
		return &remoteEnv{target: target, connection: connection, shell: r.Remote, cache: &r.pathCache}, nil
	}
	return nil, Errorf(404, "目标环境不存在：%s", id)
}

// ---- local ------------------------------------------------------------

type localEnv struct {
	target Target
	once   sync.Once
	paths  Paths
	err    error
}

func (e *localEnv) Target() Target { return e.target }
func (e *localEnv) IsLocal() bool  { return true }

func (e *localEnv) Paths(context.Context) (Paths, error) {
	e.once.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			e.err = Errorf(500, "无法确定用户目录")
			return
		}
		home = filepath.ToSlash(home)
		paths := Paths{Home: home, Codex: home + "/.codex", Grok: home + "/.grok", Agents: home + "/.agents", Hermes: home + "/.hermes"}
		if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
			paths.Codex = filepath.ToSlash(value)
		}
		if value := strings.TrimSpace(os.Getenv("HERMES_HOME")); value != "" {
			paths.Hermes = filepath.ToSlash(value)
		} else if base := os.Getenv("LOCALAPPDATA"); base != "" {
			if info, statErr := os.Stat(filepath.Join(base, "hermes")); statErr == nil && info.IsDir() {
				paths.Hermes = filepath.ToSlash(filepath.Join(base, "hermes"))
			}
		}
		e.paths = paths
	})
	return e.paths, e.err
}

func (e *localEnv) Read(_ context.Context, name string) ([]byte, error) {
	return os.ReadFile(filepath.FromSlash(name))
}

func (e *localEnv) Write(_ context.Context, name string, data []byte, mode fs.FileMode) error {
	target := filepath.FromSlash(name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".astrorder-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		temp.Close()
		if !committed {
			os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tempName, mode)
	if err := os.Rename(tempName, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func (e *localEnv) Remove(_ context.Context, name string) error {
	if err := os.Remove(filepath.FromSlash(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (e *localEnv) Shell(context.Context, string, []byte, ...string) (Result, error) {
	return Result{}, Errorf(501, "本机不支持 shell 脚本")
}

func (e *localEnv) Which(ctx context.Context, name string) (string, bool) {
	paths, err := e.Paths(ctx)
	if err != nil {
		return "", false
	}
	found, ok := lookPath(name, paths.Home)
	return filepath.ToSlash(found), ok
}

func (e *localEnv) Run(ctx context.Context, command Command) (Result, error) {
	paths, err := e.Paths(ctx)
	if err != nil {
		return Result{}, err
	}
	executable, ok := lookPath(command.Name, paths.Home)
	if !ok {
		return Result{}, Errorf(404, "未找到可执行文件：%s", command.Name)
	}
	cmd := hiddenCommand(ctx, executable, command.Args...)
	cmd.Env = os.Environ()
	for key, value := range command.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = bytes.NewReader(command.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, Errorf(504, "命令执行超时：%s", command.Name)
	}
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exit):
		result.Code = exit.ExitCode()
	default:
		return Result{}, Errorf(502, "无法启动 %s：%v", command.Name, runErr)
	}
	return result, nil
}

func extraBinDirs(home string) []string {
	dirs := []string{}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		dirs = append(dirs, filepath.ToSlash(base)+"/hermes/hermes-agent/venv/Scripts")
	}
	dirs = append(dirs, home+"/.hermes/hermes-agent/venv/bin")
	dirs = append(dirs, []string{home + "/.local/bin", home + "/bin", home + "/.cargo/bin", home + "/.vite-plus/bin", home + "/.grok/bin", "/usr/local/bin", "/opt/homebrew/bin"}...)
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		base = filepath.ToSlash(base)
		dirs = append(dirs, base+"/Microsoft/WinGet/Links", base+"/Programs/rtk")
	}
	if base := os.Getenv("APPDATA"); base != "" {
		dirs = append(dirs, filepath.ToSlash(base)+"/npm")
	}
	if base := os.Getenv("ProgramFiles"); base != "" {
		dirs = append(dirs, filepath.ToSlash(base)+"/nodejs")
	}
	return dirs
}

func lookPath(name, home string) (string, bool) {
	if strings.ContainsAny(name, `/\`) {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			return name, true
		}
		return "", false
	}
	var dirs []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range extraBinDirs(home) {
		dirs = append(dirs, filepath.FromSlash(dir))
	}
	suffixes := []string{""}
	if runtime.GOOS == "windows" {
		suffixes = nil
		for _, ext := range strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";") {
			if ext != "" {
				suffixes = append(suffixes, ext)
			}
		}
		if len(suffixes) == 0 {
			suffixes = []string{".exe", ".cmd", ".bat"}
		}
	}
	for _, dir := range dirs {
		for _, suffix := range suffixes {
			candidate := filepath.Join(dir, name+suffix)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
		}
	}
	return "", false
}

// ---- remote -----------------------------------------------------------

type remoteEnv struct {
	target     Target
	connection string
	shell      RemoteShell
	cache      *sync.Map
}

func (e *remoteEnv) Target() Target { return e.target }
func (e *remoteEnv) IsLocal() bool  { return false }

func (e *remoteEnv) Shell(ctx context.Context, script string, stdin []byte, args ...string) (Result, error) {
	result, err := e.shell.Shell(ctx, e.connection, stdin, script, args...)
	if err != nil {
		var failure *targetfs.Error
		if errors.As(err, &failure) {
			return Result{}, Errorf(502, "%s：%s", e.target.Name, failure.Detail)
		}
		return Result{}, err
	}
	return Result{Stdout: string(result.Stdout), Stderr: string(result.Stderr), Code: result.ExitCode}, nil
}

func (e *remoteEnv) Paths(ctx context.Context) (Paths, error) {
	if e.cache != nil {
		if cached, ok := e.cache.Load(e.target.ID); ok {
			return cached.(Paths), nil
		}
	}
	result, err := e.Shell(ctx, `printf %s "$HOME"`, nil)
	if err != nil {
		return Paths{}, err
	}
	home := strings.TrimSpace(result.Stdout)
	if result.Code != 0 || !strings.HasPrefix(home, "/") {
		return Paths{}, Errorf(502, "%s：无法确定用户目录", e.target.Name)
	}
	paths := Paths{Home: home, Codex: home + "/.codex", Hermes: home + "/.hermes", Grok: home + "/.grok", Agents: home + "/.agents"}
	if profile := e.target.Profile; profile != "" && profile != "default" && !strings.ContainsAny(profile, "/\\ ") {
		if e.IsDir(ctx, paths.Hermes+"/profiles/"+profile) {
			paths.Hermes += "/profiles/" + profile
		}
	}
	if e.cache != nil {
		e.cache.Store(e.target.ID, paths)
	}
	return paths, nil
}

const (
	readScript   = `if [ -f "$1" ]; then cat -- "$1"; else exit 3; fi`
	writeScript  = `d=$(dirname -- "$1"); mkdir -p -- "$d" || exit 1; t="$1.astrorder.$$"; cat > "$t" || { rm -f -- "$t"; exit 1; }; if [ -f "$1" ]; then chmod --reference="$1" "$t" 2>/dev/null || true; else chmod "$2" "$t" || true; fi; mv -f -- "$t" "$1"`
	removeScript = `rm -f -- "$1"`
	runScript    = `PATH="$HOME/.local/bin:$HOME/.hermes/hermes-agent/venv/bin:$HOME/.vite-plus/bin:$HOME/.cargo/bin:$HOME/bin:$HOME/.grok/bin:/usr/local/bin:$PATH"; export PATH; exec env "$@"`
)

func (e *remoteEnv) Read(ctx context.Context, name string) ([]byte, error) {
	result, err := e.Shell(ctx, readScript, nil, name)
	if err != nil {
		return nil, err
	}
	switch result.Code {
	case 0:
		return []byte(result.Stdout), nil
	case 3:
		return nil, ErrNotExist
	}
	return nil, Errorf(502, "%s：读取 %s 失败", e.target.Name, name)
}

func (e *remoteEnv) Write(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	result, err := e.Shell(ctx, writeScript, data, name, fmt.Sprintf("%04o", mode.Perm()))
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return Errorf(502, "%s：写入 %s 失败", e.target.Name, name)
	}
	return nil
}

func (e *remoteEnv) Remove(ctx context.Context, name string) error {
	result, err := e.Shell(ctx, removeScript, nil, name)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return Errorf(502, "%s：删除 %s 失败", e.target.Name, name)
	}
	return nil
}

func (e *remoteEnv) Which(ctx context.Context, name string) (string, bool) {
	result, err := e.Shell(ctx, `PATH="$HOME/.local/bin:$HOME/.hermes/hermes-agent/venv/bin:$HOME/.vite-plus/bin:$HOME/.cargo/bin:$HOME/bin:$HOME/.grok/bin:/usr/local/bin:$PATH"; command -v -- "$1"`, nil, name)
	if err != nil || result.Code != 0 {
		return "", false
	}
	found := strings.TrimSpace(result.Stdout)
	return found, strings.HasPrefix(found, "/")
}

func (e *remoteEnv) Run(ctx context.Context, command Command) (Result, error) {
	args := make([]string, 0, len(command.Env)+1+len(command.Args))
	keys := make([]string, 0, len(command.Env))
	for key := range command.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key+"="+command.Env[key])
	}
	args = append(args, command.Name)
	args = append(args, command.Args...)
	return e.Shell(ctx, runScript, command.Stdin, args...)
}

// Join builds a slash-separated path.
func Join(parts ...string) string { return path.Join(parts...) }

// ---- many files, trees, directories -----------------------------------

const readManyScript = `for f in "$@"; do if [ -f "$f" ]; then printf 'F %s ' "$f"; base64 < "$f" | tr -d '\n'; printf '\n'; else printf 'N %s\n' "$f"; fi; done`

func (e *remoteEnv) ReadMany(ctx context.Context, names []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(names) == 0 {
		return out, nil
	}
	result, err := e.Shell(ctx, readManyScript, nil, names...)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, Errorf(502, "%s：读取配置失败", e.target.Name)
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if !strings.HasPrefix(line, "F ") {
			continue
		}
		rest := line[2:]
		cut := strings.LastIndexByte(rest, ' ')
		if cut < 0 {
			continue
		}
		data, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[cut+1:]))
		if decodeErr != nil {
			return nil, Errorf(502, "%s：配置内容无法解码", e.target.Name)
		}
		out[rest[:cut]] = data
	}
	return out, nil
}

func (e *localEnv) ReadMany(_ context.Context, names []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range names {
		data, err := os.ReadFile(filepath.FromSlash(name))
		switch {
		case err == nil:
			out[name] = data
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, Errorf(500, "读取 %s 失败：%v", name, err)
		}
	}
	return out, nil
}

func (e *localEnv) RemoveAll(_ context.Context, name string) error {
	return os.RemoveAll(filepath.FromSlash(name))
}

func (e *localEnv) IsDir(_ context.Context, name string) bool {
	info, err := os.Stat(filepath.FromSlash(name))
	return err == nil && info.IsDir()
}

func (e *remoteEnv) RemoveAll(ctx context.Context, name string) error {
	result, err := e.Shell(ctx, `rm -rf -- "$1"`, nil, name)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return Errorf(502, "%s：删除 %s 失败", e.target.Name, name)
	}
	return nil
}

func (e *remoteEnv) IsDir(ctx context.Context, name string) bool {
	result, err := e.Shell(ctx, `[ -d "$1" ]`, nil, name)
	return err == nil && result.Code == 0
}

func (e *localEnv) ListDir(_ context.Context, dir string) ([]DirEntry, error) {
	entries, err := os.ReadDir(filepath.FromSlash(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Errorf(500, "读取目录 %s 失败：%v", dir, err)
	}
	out := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		isDir := entry.IsDir()
		if !isDir && entry.Type()&fs.ModeSymlink != 0 {
			if info, statErr := os.Stat(filepath.Join(filepath.FromSlash(dir), entry.Name())); statErr == nil {
				isDir = info.IsDir()
			}
		}
		out = append(out, DirEntry{Name: entry.Name(), Dir: isDir})
	}
	return out, nil
}

const listDirScript = `for f in "$1"/* "$1"/.[!.]*; do [ -e "$f" ] || continue; if [ -d "$f" ]; then printf 'D %s\n' "${f##*/}"; else printf 'F %s\n' "${f##*/}"; fi; done`

func (e *remoteEnv) ListDir(ctx context.Context, dir string) ([]DirEntry, error) {
	result, err := e.Shell(ctx, listDirScript, nil, dir)
	if err != nil {
		return nil, err
	}
	var out []DirEntry
	for _, line := range strings.Split(result.Stdout, "\n") {
		if len(line) > 2 && (line[0] == 'D' || line[0] == 'F') && line[1] == ' ' {
			out = append(out, DirEntry{Name: line[2:], Dir: line[0] == 'D'})
		}
	}
	return out, nil
}

// findMarker starts every hit in the remote scan output.
const findMarker = "@@ASTRORDER-FILE@@"

const findScript = `name="$1"; depth="$2"; head="$3"; shift 3
for root in "$@"; do
  [ -d "$root" ] || continue
  find -L "$root" -maxdepth "$depth" -name "$name" -type f 2>/dev/null | while IFS= read -r f; do
    printf '@@ASTRORDER-FILE@@%s\n' "$f"; head -n "$head" "$f" 2>/dev/null; printf '\n'
  done
done`

func (e *remoteEnv) Find(ctx context.Context, roots []string, filename string, depth, head int) ([]Found, error) {
	args := append([]string{filename, fmt.Sprint(depth), fmt.Sprint(head)}, roots...)
	result, err := e.Shell(ctx, findScript, nil, args...)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, Errorf(502, "%s：扫描目录失败", e.target.Name)
	}
	return ParseFound(result.Stdout), nil
}

// ParseFound reads the output of findScript.
func ParseFound(text string) []Found {
	var out []Found
	chunks := strings.Split(text, findMarker)
	if len(chunks) < 2 {
		return nil
	}
	for _, chunk := range chunks[1:] {
		line, body, _ := strings.Cut(chunk, "\n")
		out = append(out, Found{Path: strings.TrimSpace(line), Head: body})
	}
	return out
}

func (e *localEnv) Find(_ context.Context, roots []string, filename string, depth, head int) ([]Found, error) {
	var out []Found
	seen := map[string]bool{}
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			switch {
			case info.IsDir() && level < depth:
				walk(full, level+1)
			case !info.IsDir() && entry.Name() == filename:
				data, err := os.ReadFile(full)
				if err != nil {
					continue
				}
				out = append(out, Found{Path: filepath.ToSlash(full), Head: firstLines(string(data), head)})
			}
		}
	}
	for _, root := range roots {
		walk(filepath.FromSlash(root), 1)
	}
	return out, nil
}

func firstLines(text string, count int) string {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "")
}

func (e *localEnv) Rename(_ context.Context, from, to string) error {
	if err := os.MkdirAll(filepath.Dir(filepath.FromSlash(to)), 0o755); err != nil {
		return Errorf(500, "创建目录失败：%v", err)
	}
	if err := os.Rename(filepath.FromSlash(from), filepath.FromSlash(to)); err != nil {
		return Errorf(500, "移动 %s 失败：%v", from, err)
	}
	return nil
}

func (e *remoteEnv) Rename(ctx context.Context, from, to string) error {
	result, err := e.Shell(ctx, `mkdir -p -- "$(dirname -- "$2")" && mv -- "$1" "$2"`, nil, from, to)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return Errorf(502, "%s：移动 %s 失败", e.target.Name, from)
	}
	return nil
}
