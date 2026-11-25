package digest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	prefDigestHistory = "digest:history"
	maxHistoryEntries = 30
)

// DigestHistoryRecord represents one archived work digest / weekly report.
type DigestHistoryRecord struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	TemplateID   string   `json:"template_id"`
	TemplateName string   `json:"template_name"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Markdown     string   `json:"markdown"`
	Model        string   `json:"model,omitempty"`
	ProjectNames []string `json:"project_names,omitempty"`
	ItemCount    int      `json:"item_count"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

func (h *handler) listHistory(ctx context.Context) ([]DigestHistoryRecord, error) {
	if h.store == nil {
		return []DigestHistoryRecord{}, nil
	}
	pref, err := h.store.GetPreference(ctx, prefDigestHistory)
	if err != nil || !pref.Exists || pref.Value == nil {
		return []DigestHistoryRecord{}, err
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return []DigestHistoryRecord{}, err
	}
	var list []DigestHistoryRecord
	if err := json.Unmarshal(raw, &list); err != nil {
		return []DigestHistoryRecord{}, nil
	}
	if list == nil {
		list = []DigestHistoryRecord{}
	}
	return list, nil
}

func (h *handler) saveHistory(ctx context.Context, rec DigestHistoryRecord) (DigestHistoryRecord, error) {
	if h.store == nil {
		return DigestHistoryRecord{}, errors.New("store unavailable")
	}
	rec.Title = strings.TrimSpace(rec.Title)
	rec.Markdown = strings.TrimSpace(rec.Markdown)
	if rec.Title == "" {
		rec.Title = "未命名工作复盘"
	}
	if rec.Markdown == "" {
		return DigestHistoryRecord{}, errors.New("报告内容不能为空")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec.ID = strings.TrimSpace(rec.ID)
	if rec.ID == "" {
		rec.ID = "digest_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000"), ".", "")
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	if rec.ProjectNames == nil {
		rec.ProjectNames = []string{}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.listHistory(ctx)
	if err != nil {
		return DigestHistoryRecord{}, err
	}
	next := make([]DigestHistoryRecord, 0, len(list)+1)
	next = append(next, rec)
	for _, existing := range list {
		if existing.ID == rec.ID {
			if existing.CreatedAt != "" {
				next[0].CreatedAt = existing.CreatedAt
			}
			continue
		}
		next = append(next, existing)
	}
	if len(next) > maxHistoryEntries {
		next = next[:maxHistoryEntries]
	}
	if err := h.store.SetPreference(ctx, prefDigestHistory, next, true); err != nil {
		return DigestHistoryRecord{}, err
	}
	return next[0], nil
}

func (h *handler) deleteHistory(ctx context.Context, id string) error {
	if h.store == nil {
		return errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("缺少记录 id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.listHistory(ctx)
	if err != nil {
		return err
	}
	next := make([]DigestHistoryRecord, 0, len(list))
	for _, item := range list {
		if item.ID != id {
			next = append(next, item)
		}
	}
	return h.store.SetPreference(ctx, prefDigestHistory, next, true)
}
