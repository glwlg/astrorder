package connections

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type upgradeJob struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Log       string `json:"log"`
	CreatedAt string `json:"created_at"`
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Target    string `json:"target"`
	mu        sync.Mutex
	cancel    context.CancelFunc
}

func (j *upgradeJob) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Log += string(p)
	return len(p), nil
}

func (j *upgradeJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{"id": j.ID, "title": j.Title, "status": j.Status, "detail": j.Detail, "log": j.Log,
		"created_at": j.CreatedAt, "current": j.Current, "total": j.Total, "target": j.Target}
}

func (h *Handler) handleUpgradeJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.upgradeMu.Lock()
		items := make([]map[string]any, 0, len(h.upgradeJobs))
		for _, job := range h.upgradeJobs {
			items = append(items, job.snapshot())
		}
		h.upgradeMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	var request struct {
		AgentIDs []string `json:"agent_ids"`
		Title    string   `json:"title"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request) != nil || len(request.AgentIDs) == 0 || len(request.AgentIDs) > 100 || len(request.Title) > 512 {
		writeError(w, 422, "invalid_request", "请选择要升级的 Agent")
		return
	}
	type target struct {
		name, kind, connectionID string
		connection               map[string]any
	}
	targets := make([]target, 0, len(request.AgentIDs))
	seen := map[string]bool{}
	for _, id := range request.AgentIDs {
		if seen[id] {
			writeError(w, 422, "invalid_request", "升级目标重复")
			return
		}
		seen[id] = true
		agent, err := h.store.GetAgent(r.Context(), id)
		if err != nil {
			writeError(w, 500, "store_error", err.Error())
			return
		}
		if agent == nil {
			writeError(w, 404, "agent_not_found", "Agent 不存在")
			return
		}
		kind, _ := agent["kind"].(string)
		switch kind {
		case "codex", "hermes", "grok", "antigravity":
		default:
			writeError(w, 422, "unsupported_agent", "该 Agent 不支持升级")
			return
		}
		connID, _ := agent["connection_id"].(string)
		name, _ := agent["name"].(string)
		var conn map[string]any
		if connID != "" && connID != "local" {
			conn, err = h.store.GetSSHConnection(r.Context(), connID)
			if err != nil {
				writeError(w, 500, "store_error", err.Error())
				return
			}
			if conn == nil {
				writeError(w, 409, "connection_removed", "Agent 所属环境已移除，无法升级")
				return
			}
		}
		targets = append(targets, target{name, kind, connID, conn})
	}
	ctx, cancel := context.WithCancel(h.upgradeContext)
	job := &upgradeJob{ID: "upgrade-" + rand.Text(), Title: request.Title, Status: "running", Detail: "正在执行版本升级…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Total: len(targets), cancel: cancel}
	if job.Title == "" {
		job.Title = "升级 Agent"
	}
	h.upgradeMu.Lock()
	h.upgradeJobs[job.ID] = job
	h.upgradeMu.Unlock()
	h.upgradeWG.Add(1)
	go func() {
		defer h.upgradeWG.Done()
		defer cancel()
		success, failed := 0, 0
		for i, target := range targets {
			if ctx.Err() != nil {
				break
			}
			job.mu.Lock()
			job.Current, job.Target = i+1, target.name
			job.Detail = fmt.Sprintf("正在升级：%s（%d/%d）", target.name, i+1, len(targets))
			job.mu.Unlock()
			fmt.Fprintf(job, "\n[%d/%d] %s\n", i+1, len(targets), target.name)
			code, err := h.upgrader.Upgrade(ctx, target.kind, target.connectionID, target.connection,
				func(command string) { fmt.Fprintf(job, "[EXEC] %s\n", command) }, job)
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				failed++
				fmt.Fprintf(job, "[ERROR] %s: %s\n", target.name, err)
			} else if code != 0 {
				failed++
				fmt.Fprintf(job, "[FAIL] %s 升级退出码: %d\n", target.name, code)
			} else {
				success++
				fmt.Fprintf(job, "[DONE] %s 升级成功\n", target.name)
			}
		}
		job.mu.Lock()
		defer job.mu.Unlock()
		job.Status = "completed"
		job.Detail = fmt.Sprintf("升级结束：成功 %d，失败 %d", success, failed)
		if ctx.Err() != nil {
			job.Status, job.Detail = "cancelled", fmt.Sprintf("升级已取消：成功 %d，失败 %d", success, failed)
		} else if failed > 0 {
			job.Status = "failed"
		}
	}()
	writeJSON(w, http.StatusAccepted, job.snapshot())
}

func (h *Handler) handleCancelUpgradeJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/agents/upgrade/jobs/"), "/cancel")
	h.upgradeMu.Lock()
	job := h.upgradeJobs[id]
	h.upgradeMu.Unlock()
	if job == nil {
		writeError(w, 404, "not_found", "升级任务不存在")
		return
	}
	job.mu.Lock()
	running := job.Status == "running"
	if running {
		job.cancel()
	}
	job.mu.Unlock()
	writeJSON(w, 200, map[string]any{"cancelled": running})
}

func (h *Handler) Close(ctx context.Context) error {
	h.upgradeCancel()
	done := make(chan struct{})
	go func() { h.upgradeWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
