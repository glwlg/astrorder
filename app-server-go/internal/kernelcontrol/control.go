// Package kernelcontrol dispatches durable user operations to an independent controller.
package kernelcontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

var ErrBusy = errors.New("另一个内核操作正在执行")
var ErrUnavailable = errors.New("内核控制器不可用，请检查安装配置")

type Operation struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Error     string    `json:"error,omitempty"`
}
type Service struct {
	mu         sync.Mutex
	StateDir   string
	Root       string
	Controller string
	Launch     func(string) error
	Run        func(context.Context, string, string) (map[string]any, error)
}

func New(configPath, appPath string) *Service {
	if configPath == "" || !filepath.IsAbs(configPath) || !filepath.IsAbs(appPath) {
		return nil
	}
	bin := filepath.Dir(appPath)
	return &Service{StateDir: filepath.Dir(configPath), Root: filepath.Dir(bin), Controller: filepath.Join(bin, "astrorderctl.exe")}
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *Service) dir() string { return filepath.Join(s.StateDir, "kernel-operations") }
func (s *Service) save(op Operation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir(), ".receipt-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(s.dir(), op.ID+".json"))
}
func (s *Service) Get(id string) (Operation, error) {
	if s == nil || !validID(id) {
		return Operation{}, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(s.dir(), id+".json"))
	if err != nil {
		return Operation{}, err
	}
	var op Operation
	err = json.Unmarshal(data, &op)
	if err == nil && (op.Status == "queued" || op.Status == "running") && time.Since(op.CreatedAt) > 3*time.Minute {
		op.Status = "failed"
		op.Error = "操作超时，结果未确认"
	}
	return op, err
}
func validAction(target, action string) bool {
	return (target == "app" || target == "daemon") && action == "restart" || target == "daemon" && action == "update"
}
func (s *Service) Start(target, action string, confirmed bool) (Operation, error) {
	if !validAction(target, action) || !confirmed {
		return Operation{}, errors.New("必须明确确认有效的内核操作")
	}
	if s == nil || s.StateDir == "" {
		return Operation{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Launch == nil {
		if _, err := os.Stat(s.Controller); err != nil {
			return Operation{}, ErrUnavailable
		}
	}
	if err := os.MkdirAll(s.dir(), 0700); err != nil {
		return Operation{}, err
	}
	active := filepath.Join(s.dir(), "active.json")
	if data, err := os.ReadFile(active); err == nil {
		var old Operation
		if json.Unmarshal(data, &old) != nil || time.Since(old.CreatedAt) < 3*time.Minute {
			return Operation{}, ErrBusy
		}
		old.Status = "failed"
		old.Error = "上次操作超时，结果未确认"
		if validID(old.ID) {
			if err = s.save(old); err != nil {
				return Operation{}, err
			}
		}
		if err = os.Remove(active); err != nil {
			return Operation{}, err
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Operation{}, err
	}
	op := Operation{ID: hex.EncodeToString(random[:]), Target: target, Action: action, Status: "queued", CreatedAt: time.Now().UTC()}
	f, err := os.OpenFile(active, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return Operation{}, ErrBusy
	}
	if err != nil {
		return Operation{}, err
	}
	err = json.NewEncoder(f).Encode(op)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(active)
		return Operation{}, err
	}
	if err = s.save(op); err != nil {
		os.Remove(active)
		return Operation{}, err
	}
	launch := s.Launch
	if launch == nil {
		launch = s.launch
	}
	if err = launch(op.ID); err != nil {
		op.Status = "failed"
		op.Error = "无法启动独立内核控制器: " + err.Error()
		_ = s.save(op)
		_ = os.Remove(active)
		return Operation{}, errors.New(op.Error)
	}
	// Completed history is bounded by count and age.
	entries, _ := os.ReadDir(s.dir())
	type receiptFile struct {
		name     string
		modified time.Time
	}
	var receipts []receiptFile
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != "active.json" && strings.HasSuffix(entry.Name(), ".json") {
			if info, e := entry.Info(); e == nil {
				receipts = append(receipts, receiptFile{entry.Name(), info.ModTime()})
			}
		}
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].modified.After(receipts[j].modified) })
	for i, entry := range receipts {
		if entry.name != op.ID+".json" && (i >= 100 || time.Since(entry.modified) > 24*time.Hour) {
			_ = os.Remove(filepath.Join(s.dir(), entry.name))
			_ = os.Remove(filepath.Join(s.dir(), strings.TrimSuffix(entry.name, ".json")+".claim"))
		}
	}
	return op, nil
}
func (s *Service) launch(id string) error {
	command := exec.Command(s.Controller, "kernel-operation", "--root", s.Root, "--state-dir", s.StateDir, "--id", id)
	command.Dir = s.Root
	if err := lifecycle.ConfigureIndependentProcess(command); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
func (s *Service) command(ctx context.Context, target, action string) (map[string]any, error) {
	args := []string{target, action, "--root", s.Root, "--state-dir", s.StateDir}
	if target == "daemon" && (action == "restart" || action == "update") {
		args = append(args, "--confirm-active")
	}
	command := exec.CommandContext(ctx, s.Controller, args...)
	if err := lifecycle.ConfigureIndependentProcess(command); err != nil {
		return nil, err
	}
	data, err := command.Output()
	var result map[string]json.RawMessage
	if e := json.Unmarshal(data, &result); e != nil {
		return nil, fmt.Errorf("内核控制器未返回有效结果: %w", e)
	}
	if err != nil {
		var detail string
		_ = json.Unmarshal(result["error"], &detail)
		if detail == "" {
			detail = "内核控制器执行失败"
		}
		return nil, errors.New(detail)
	}
	var value map[string]any
	if e := json.Unmarshal(result[target], &value); e != nil || value == nil {
		return nil, errors.New("内核控制器缺少状态结果")
	}
	return value, nil
}
func (s *Service) runner() func(context.Context, string, string) (map[string]any, error) {
	if s.Run != nil {
		return s.Run
	}
	return s.command
}
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	run := s.runner()
	result := map[string]any{}
	for _, pair := range [][2]string{{"app", "status"}, {"daemon", "status"}, {"daemon", "update-check"}} {
		value, err := run(ctx, pair[0], pair[1])
		if err != nil {
			return nil, err
		}
		key := pair[0]
		if pair[1] == "update-check" {
			key = "update"
		}
		result[key] = value
	}
	return result, nil
}
func (s *Service) Execute(ctx context.Context, id string) (err error) {
	op, err := s.Get(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.dir(), "active.json"))
	if err != nil {
		return err
	}
	var active Operation
	if json.Unmarshal(data, &active) != nil || active.ID != id || op.Status != "queued" || !validAction(op.Target, op.Action) || time.Since(op.CreatedAt) > 3*time.Minute {
		return errors.New("操作已失效")
	}
	claim, claimErr := os.OpenFile(filepath.Join(s.dir(), id+".claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if claimErr != nil {
		return errors.New("操作执行器已启动")
	}
	if claimErr = claim.Close(); claimErr != nil {
		return claimErr
	}
	op.Status = "running"
	if err = s.save(op); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			op.Status = "failed"
			op.Error = err.Error()
		} else {
			op.Status = "success"
		}
		if saveErr := s.save(op); saveErr != nil {
			err = saveErr
		}
		if data, readErr := os.ReadFile(filepath.Join(s.dir(), "active.json")); readErr == nil {
			var current Operation
			if json.Unmarshal(data, &current) == nil && current.ID == id {
				_ = os.Remove(filepath.Join(s.dir(), "active.json"))
			}
		}
	}()
	run := s.runner()
	before, err := run(ctx, op.Target, "status")
	if err != nil {
		return err
	}
	if before["identity_verified"] != true {
		return errors.New("无法确认原内核进程身份")
	}
	if op.Action == "update" {
		update, e := run(ctx, "daemon", "update-check")
		if e != nil {
			return e
		}
		if update["update_available"] != true {
			return errors.New("没有可用的小内核更新")
		}
	}
	_, actionErr := run(ctx, op.Target, op.Action)
	after, statusErr := run(ctx, op.Target, "status")
	// Windows may deny process inspection briefly while the old image exits.
	// Retry the authority read; never infer a stopped process from that error.
	for attempt := 0; statusErr != nil && op.Target == "app" && actionErr != nil && attempt < 20; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		after, statusErr = run(ctx, op.Target, "status")
	}
	if statusErr != nil {
		return statusErr
	}
	if actionErr != nil && op.Target == "app" && after["state"] == "stopped" && after["port_owned"] == false && after["identity_verified"] == false {
		// A controller may lose query access while its exact old App exits.
		// Start only after authority confirms no owned process or listener.
		_, actionErr = run(ctx, "app", "start")
		after, statusErr = run(ctx, "app", "status")
		if statusErr != nil {
			return statusErr
		}
	}
	if after["state"] != "running" || after["identity_verified"] != true || after["port_owned"] != true || op.Target == "app" && after["healthy"] != true || reflect.DeepEqual(before["process"], after["process"]) {
		if actionErr != nil {
			return actionErr
		}
		return errors.New("未确认新内核健康运行")
	}
	if actionErr != nil {
		return actionErr
	}
	if op.Action == "update" {
		check, e := run(ctx, "daemon", "update-check")
		if e != nil {
			return e
		}
		if check["update_available"] == true {
			return errors.New("小内核更新尚未完成")
		}
	}
	return nil
}
