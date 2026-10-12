// Package envtargettest provides an in-memory envtarget.Env for tests.
package envtargettest

import (
	"context"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/envtarget"
)

// RunFunc answers a command; a nil result means "not found".
type RunFunc func(command envtarget.Command, env *Env) (envtarget.Result, error)

// Env keeps files in memory and answers commands with Run.
type Env struct {
	Info    envtarget.Target
	P       envtarget.Paths
	Files   map[string][]byte
	Dirs    map[string]bool
	Bins    map[string]string
	Run_    RunFunc
	Calls   []envtarget.Command
	FailOn  map[string]error // path -> error returned by Write
	mu      sync.Mutex
	Writes  []string
	Packed  map[string][]byte
	Unpacks map[string][]byte
}

func New(id string) *Env {
	home := "/home/test"
	return &Env{
		Info:    envtarget.Target{ID: id, Kind: "ssh", Name: id},
		P:       envtarget.Paths{Home: home, Codex: home + "/.codex", Hermes: home + "/.hermes", Grok: home + "/.grok", Agents: home + "/.agents"},
		Files:   map[string][]byte{},
		Dirs:    map[string]bool{},
		Bins:    map[string]string{},
		FailOn:  map[string]error{},
		Packed:  map[string][]byte{},
		Unpacks: map[string][]byte{},
	}
}

func (e *Env) Target() envtarget.Target { return e.Info }
func (e *Env) IsLocal() bool            { return e.Info.Kind == "local" }
func (e *Env) Paths(context.Context) (envtarget.Paths, error) {
	return e.P, nil
}

func (e *Env) Read(_ context.Context, name string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, ok := e.Files[name]
	if !ok {
		return nil, envtarget.ErrNotExist
	}
	return append([]byte{}, data...), nil
}

func (e *Env) Write(_ context.Context, name string, data []byte, _ fs.FileMode) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.FailOn[name]; err != nil {
		return err
	}
	e.Files[name] = append([]byte{}, data...)
	e.Writes = append(e.Writes, name)
	return nil
}

func (e *Env) Remove(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.Files, name)
	return nil
}

func (e *Env) RemoveAll(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key := range e.Files {
		if key == name || strings.HasPrefix(key, name+"/") {
			delete(e.Files, key)
		}
	}
	delete(e.Dirs, name)
	return nil
}

func (e *Env) IsDir(_ context.Context, name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Dirs[name] {
		return true
	}
	for key := range e.Files {
		if strings.HasPrefix(key, name+"/") {
			return true
		}
	}
	return false
}

func (e *Env) ReadMany(ctx context.Context, names []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range names {
		if data, err := e.Read(ctx, name); err == nil {
			out[name] = data
		}
	}
	return out, nil
}

func (e *Env) ListDir(_ context.Context, dir string) ([]envtarget.DirEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[string]bool{}
	var out []envtarget.DirEntry
	for key := range e.Files {
		rest, ok := strings.CutPrefix(key, dir+"/")
		if !ok {
			continue
		}
		first, more, nested := strings.Cut(rest, "/")
		_ = more
		if !seen[first] {
			seen[first] = true
			out = append(out, envtarget.DirEntry{Name: first, Dir: nested})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (e *Env) Find(_ context.Context, roots []string, filename string, depth, head int) ([]envtarget.Found, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []envtarget.Found
	for key, data := range e.Files {
		if path.Base(key) != filename {
			continue
		}
		for _, root := range roots {
			rest, ok := strings.CutPrefix(key, root+"/")
			if !ok || strings.Count(rest, "/")+1 > depth-0 {
				continue
			}
			lines := strings.SplitAfter(string(data), "\n")
			if len(lines) > head {
				lines = lines[:head]
			}
			out = append(out, envtarget.Found{Path: key, Head: strings.Join(lines, "")})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (e *Env) Which(_ context.Context, name string) (string, bool) {
	found, ok := e.Bins[name]
	return found, ok
}

func (e *Env) Shell(context.Context, string, []byte, ...string) (envtarget.Result, error) {
	return envtarget.Result{}, envtarget.Errorf(501, "not supported")
}

func (e *Env) Run(_ context.Context, command envtarget.Command) (envtarget.Result, error) {
	e.mu.Lock()
	e.Calls = append(e.Calls, command)
	e.mu.Unlock()
	known := false
	for name, full := range e.Bins {
		known = known || name == command.Name || full == command.Name
	}
	if !known {
		return envtarget.Result{}, envtarget.Errorf(404, "未找到可执行文件：%s", command.Name)
	}
	if e.Run_ == nil {
		return envtarget.Result{}, nil
	}
	return e.Run_(command, e)
}

func (e *Env) PackDir(_ context.Context, dir string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, ok := e.Packed[dir]
	if !ok {
		return nil, envtarget.ErrNotExist
	}
	return data, nil
}

func (e *Env) UnpackDir(_ context.Context, parent, name string, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Unpacks[parent+"/"+name] = data
	e.Files[parent+"/"+name+"/SKILL.md"] = []byte("---\nname: " + name + "\n---\n")
	return nil
}

// Opener serves fixed environments.
type Opener struct {
	Envs []*Env
}

func (o Opener) List(context.Context) ([]envtarget.Target, error) {
	out := make([]envtarget.Target, 0, len(o.Envs))
	for _, env := range o.Envs {
		out = append(out, env.Info)
	}
	return out, nil
}

func (o Opener) Open(_ context.Context, id string) (envtarget.Env, error) {
	for _, env := range o.Envs {
		if env.Info.ID == id {
			return env, nil
		}
	}
	return nil, envtarget.Errorf(404, "目标环境不存在：%s", id)
}

func (e *Env) Rename(_ context.Context, from, to string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, data := range e.Files {
		if key == from || strings.HasPrefix(key, from+"/") {
			e.Files[to+strings.TrimPrefix(key, from)] = data
			delete(e.Files, key)
		}
	}
	return nil
}
