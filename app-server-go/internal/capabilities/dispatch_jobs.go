package capabilities

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

const (
	maxDispatchJobHistory     = 30
	maxDispatchJobItems       = 64
	maxDispatchJobTotalSteps  = 256
	dispatchJobMaxConcurrency = 3
)

type DispatchJobItem struct {
	ItemID      string `json:"item_id"`
	SourceAgent string `json:"source_agent"`
	Label       string `json:"label,omitempty"`
}

type DispatchJobReport struct {
	ItemID        string `json:"item_id"`
	ItemLabel     string `json:"item_label,omitempty"`
	TargetID      string `json:"target_id"`
	TargetName    string `json:"target_name,omitempty"`
	Agent         string `json:"agent"`
	OK            bool   `json:"ok"`
	InstalledPath string `json:"installed_path,omitempty"`
	Message       string `json:"message"`
}

type DispatchJob struct {
	ID             string              `json:"id"`
	Status         string              `json:"status"` // running | completed | partial | failed
	Type           string              `json:"capability_type"`
	SourceTargetID string              `json:"source_target_id"`
	SourceTarget   string              `json:"source_target_name,omitempty"`
	Items          []DispatchJobItem   `json:"items"`
	TargetMatrix   []DispatchTarget    `json:"target_matrix"`
	TotalSteps     int                 `json:"total_steps"`
	CompletedSteps int                 `json:"completed_steps"`
	SuccessCount   int                 `json:"success_count"`
	FailedCount    int                 `json:"failed_count"`
	CurrentLabel   string              `json:"current_label,omitempty"`
	Reports        []DispatchJobReport `json:"reports"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	FinishedAt     string              `json:"finished_at,omitempty"`
}

func newDispatchJobID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "job-" + time.Now().UTC().Format("20060102150405.000000")
	}
	return "job-" + hex.EncodeToString(raw[:])
}

func cloneDispatchJob(j *DispatchJob) *DispatchJob {
	if j == nil {
		return nil
	}
	cp := *j
	cp.Items = make([]DispatchJobItem, len(j.Items))
	copy(cp.Items, j.Items)
	cp.TargetMatrix = make([]DispatchTarget, len(j.TargetMatrix))
	copy(cp.TargetMatrix, j.TargetMatrix)
	cp.Reports = make([]DispatchJobReport, len(j.Reports))
	copy(cp.Reports, j.Reports)
	return &cp
}

func dedupeDispatchItems(items []DispatchJobItem) []DispatchJobItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]DispatchJobItem, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ItemID)
		if id == "" {
			continue
		}
		sa := strings.TrimSpace(item.SourceAgent)
		key := sa + "\x00" + id
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = id
		}
		out = append(out, DispatchJobItem{ItemID: id, SourceAgent: sa, Label: label})
	}
	return out
}

func dedupeDispatchMatrix(matrix []DispatchTarget) []DispatchTarget {
	seen := make(map[string]struct{}, len(matrix))
	out := make([]DispatchTarget, 0, len(matrix))
	for _, entry := range matrix {
		tid := strings.TrimSpace(entry.TargetID)
		ag := strings.TrimSpace(entry.Agent)
		if tid == "" || ag == "" {
			continue
		}
		key := tid + "\x00" + ag
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, DispatchTarget{TargetID: tid, Agent: ag})
	}
	return out
}

// StartDispatchJob validates a single- or multi-item dispatch request and launches
// it asynchronously on a detached background context so it keeps running even if
// the caller closes the settings window or refreshes the page.
func (s *Service) StartDispatchJob(
	ctx context.Context,
	kind, sourceTarget string,
	rawItems []DispatchJobItem,
	rawMatrix []DispatchTarget,
) (*DispatchJob, error) {
	switch kind {
	case "skill", "mcp", "plugin":
	default:
		return nil, envtarget.Errorf(422, "不支持的能力类型：%s", kind)
	}
	items := dedupeDispatchItems(rawItems)
	matrix := dedupeDispatchMatrix(rawMatrix)
	if len(items) == 0 || len(matrix) == 0 {
		return nil, envtarget.Errorf(422, "请选择至少一个要分发的项目和一个目标")
	}
	if len(items) > maxDispatchJobItems {
		return nil, envtarget.Errorf(422, "单次批量分发最多支持 %d 个项目", maxDispatchJobItems)
	}
	if len(matrix) > 64 {
		return nil, envtarget.Errorf(422, "单次分发最多支持 64 个目标节点与 Agent")
	}
	totalSteps := len(items) * len(matrix)
	if totalSteps > maxDispatchJobTotalSteps {
		return nil, envtarget.Errorf(422, "单次后台分发总步数不能超过 %d 步", maxDispatchJobTotalSteps)
	}

	sourceTargetName := sourceTarget
	if s.Targets != nil {
		if targets, err := s.Targets.List(ctx); err == nil {
			for _, t := range targets {
				if t.ID == sourceTarget && t.Name != "" {
					sourceTargetName = t.Name
					break
				}
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	job := &DispatchJob{
		ID:             newDispatchJobID(),
		Status:         "running",
		Type:           kind,
		SourceTargetID: sourceTarget,
		SourceTarget:   sourceTargetName,
		Items:          items,
		TargetMatrix:   matrix,
		TotalSteps:     totalSteps,
		CurrentLabel:   items[0].Label,
		Reports:        []DispatchJobReport{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	s.dispatchJobsMu.Lock()
	s.dispatchJobs = append([]*DispatchJob{job}, s.dispatchJobs...)
	if len(s.dispatchJobs) > maxDispatchJobHistory {
		s.dispatchJobs = s.dispatchJobs[:maxDispatchJobHistory]
	}
	snap := cloneDispatchJob(job)
	s.dispatchJobsMu.Unlock()

	go s.runDispatchJob(job, kind, sourceTarget, items, matrix)
	return snap, nil
}

func (s *Service) runDispatchJob(
	job *DispatchJob,
	kind, sourceTarget string,
	items []DispatchJobItem,
	matrix []DispatchTarget,
) {
	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	recordReport := func(rep DispatchJobReport) {
		s.dispatchJobsMu.Lock()
		job.Reports = append(job.Reports, rep)
		job.CompletedSteps++
		if rep.OK {
			job.SuccessCount++
		} else {
			job.FailedCount++
		}
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.dispatchJobsMu.Unlock()
	}

	setCurrent := func(label string) {
		s.dispatchJobsMu.Lock()
		job.CurrentLabel = label
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.dispatchJobsMu.Unlock()
	}

	source, srcErr := s.open(jobCtx, sourceTarget)
	if srcErr != nil {
		_, errMsg := statusOf(srcErr)
		for _, item := range items {
			for _, entry := range matrix {
				recordReport(DispatchJobReport{
					ItemID:    item.ItemID,
					ItemLabel: item.Label,
					TargetID:  entry.TargetID,
					Agent:     entry.Agent,
					OK:        false,
					Message:   errMsg,
				})
			}
		}
	} else {
		for _, item := range items {
			setCurrent(item.Label)
			var deliver func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error)
			var prepErr error
			switch kind {
			case "skill":
				deliver, prepErr = s.skillDelivery(jobCtx, source, item.SourceAgent, item.ItemID)
			case "mcp":
				deliver, prepErr = s.mcpDelivery(jobCtx, source, item.SourceAgent, item.ItemID)
			case "plugin":
				deliver, prepErr = s.pluginDelivery(jobCtx, source, item.SourceAgent, item.ItemID)
			}
			if prepErr != nil {
				_, errMsg := statusOf(prepErr)
				for _, entry := range matrix {
					recordReport(DispatchJobReport{
						ItemID:    item.ItemID,
						ItemLabel: item.Label,
						TargetID:  entry.TargetID,
						Agent:     entry.Agent,
						OK:        false,
						Message:   errMsg,
					})
				}
				continue
			}

			sem := make(chan struct{}, dispatchJobMaxConcurrency)
			var wg sync.WaitGroup
			for _, entry := range matrix {
				wg.Add(1)
				sem <- struct{}{}
				go func(target DispatchTarget) {
					defer wg.Done()
					defer func() { <-sem }()

					report := DispatchJobReport{
						ItemID:    item.ItemID,
						ItemLabel: item.Label,
						TargetID:  target.TargetID,
						Agent:     target.Agent,
					}
					dest, openErr := s.open(jobCtx, target.TargetID)
					switch {
					case !validAgent(target.Agent):
						report.Message = "不支持的 Agent：" + target.Agent
					case openErr != nil:
						_, report.Message = statusOf(openErr)
					default:
						report.TargetName = dest.Target().Name
						where, message, deliverErr := deliver(jobCtx, dest, target.Agent)
						report.InstalledPath, report.Message = where, message
						if deliverErr != nil {
							_, report.Message = statusOf(deliverErr)
						} else {
							report.OK = true
						}
					}
					recordReport(report)
				}(entry)
			}
			wg.Wait()
		}
	}

	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	s.dispatchJobsMu.Lock()
	switch {
	case job.FailedCount == 0 && job.SuccessCount > 0:
		job.Status = "completed"
	case job.SuccessCount > 0 && job.FailedCount > 0:
		job.Status = "partial"
	default:
		job.Status = "failed"
	}
	job.CurrentLabel = ""
	job.UpdatedAt = finishedAt
	job.FinishedAt = finishedAt
	s.dispatchJobsMu.Unlock()
}

func (s *Service) ListDispatchJobs() []*DispatchJob {
	s.dispatchJobsMu.RLock()
	defer s.dispatchJobsMu.RUnlock()
	out := make([]*DispatchJob, 0, len(s.dispatchJobs))
	for _, j := range s.dispatchJobs {
		out = append(out, cloneDispatchJob(j))
	}
	return out
}

func (s *Service) GetDispatchJob(id string) (*DispatchJob, bool) {
	s.dispatchJobsMu.RLock()
	defer s.dispatchJobsMu.RUnlock()
	for _, j := range s.dispatchJobs {
		if j.ID == id {
			return cloneDispatchJob(j), true
		}
	}
	return nil, false
}

func (s *Service) ClearFinishedDispatchJobs() (int, []*DispatchJob) {
	s.dispatchJobsMu.Lock()
	defer s.dispatchJobsMu.Unlock()
	kept := make([]*DispatchJob, 0, len(s.dispatchJobs))
	cleared := 0
	for _, j := range s.dispatchJobs {
		if j.Status == "running" {
			kept = append(kept, j)
		} else {
			cleared++
		}
	}
	s.dispatchJobs = kept
	out := make([]*DispatchJob, 0, len(kept))
	for _, j := range kept {
		out = append(out, cloneDispatchJob(j))
	}
	return cleared, out
}
