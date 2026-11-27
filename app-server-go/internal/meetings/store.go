package meetings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
)

const (
	PrefMeetingsRecords  = "meetings:records"
	PrefMeetingsSettings = "meetings:settings"
	maxStoredMeetings    = 200
)

func randomID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func normalizePriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "high", "高", "p0", "p1":
		return "high"
	case "low", "低", "p3":
		return "low"
	default:
		return "medium"
	}
}

func normalizeStatus(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "done") {
		return "done"
	}
	return "open"
}

func cleanStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, s := range items {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func cleanTopics(items []MeetingTopic) []MeetingTopic {
	out := make([]MeetingTopic, 0, len(items))
	for _, t := range items {
		title := strings.TrimSpace(t.Title)
		detail := strings.TrimSpace(t.Detail)
		if title == "" && detail == "" {
			continue
		}
		if title == "" {
			title = detail
			detail = ""
		}
		out = append(out, MeetingTopic{
			Timestamp: strings.TrimSpace(t.Timestamp),
			Title:     title,
			Detail:    detail,
		})
	}
	return out
}

func maskSecret(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:3] + "••••••••" + s[len(s)-4:]
}

func defaultMeetingSettings() MeetingSettings {
	return MeetingSettings{
		ModelSource:              "global",
		BaseURL:                  "https://api.deepseek.com/v1",
		Model:                    "deepseek-chat",
		Reasoning:                "low",
		AutoSummarize:            true,
		AutoSummarizeIntervalSec: 30,
		SummaryStyle:             "concise",
	}
}

func normalizeMeetingSettings(in MeetingSettings) MeetingSettings {
	out := in
	switch strings.ToLower(strings.TrimSpace(out.ModelSource)) {
	case "provider":
		out.ModelSource = "provider"
	case "custom":
		out.ModelSource = "custom"
	default:
		out.ModelSource = "global"
	}
	out.ProviderID = strings.TrimSpace(out.ProviderID)
	out.BaseURL = strings.TrimSpace(out.BaseURL)
	if out.BaseURL == "" {
		out.BaseURL = "https://api.deepseek.com/v1"
	}
	out.Model = strings.TrimSpace(out.Model)
	if out.Model == "" && out.ModelSource != "provider" {
		out.Model = "deepseek-chat"
	}
	out.Reasoning = strings.TrimSpace(out.Reasoning)
	if out.Reasoning == "" {
		out.Reasoning = "low"
	}
	switch out.AutoSummarizeIntervalSec {
	case 20, 30, 60:
	default:
		out.AutoSummarizeIntervalSec = 30
	}
	switch strings.ToLower(strings.TrimSpace(out.SummaryStyle)) {
	case "detailed", "technical":
		out.SummaryStyle = strings.ToLower(strings.TrimSpace(out.SummaryStyle))
	default:
		out.SummaryStyle = "concise"
	}
	return out
}

func (h *handler) loadRawSettings(ctx context.Context) (MeetingSettings, error) {
	def := defaultMeetingSettings()
	if h.store == nil {
		return def, nil
	}
	pref, err := h.store.GetPreference(ctx, PrefMeetingsSettings)
	if err != nil {
		return def, err
	}
	if !pref.Exists || pref.Value == nil {
		return def, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return def, err
	}
	var saved MeetingSettings
	if err := json.Unmarshal(raw, &saved); err != nil {
		return def, nil
	}
	return normalizeMeetingSettings(saved), nil
}

func (h *handler) getPublicSettings(ctx context.Context) (MeetingSettings, error) {
	raw, err := h.loadRawSettings(ctx)
	if err != nil {
		return MeetingSettings{}, err
	}
	return h.decoratePublicSettings(ctx, raw), nil
}

func (h *handler) decoratePublicSettings(ctx context.Context, raw MeetingSettings) MeetingSettings {
	globalCfg := aiprovider.ResolveDefault(ctx, h.store)
	globalModel := strings.TrimSpace(globalCfg.Model)
	globalReady := globalCfg.IsConfigured() && (globalCfg.Protocol == "agent" || globalModel != "")
	customKey := strings.TrimSpace(raw.APIKey)

	pub := raw
	pub.APIKey = ""
	pub.APIKeyClear = false
	pub.APIKeySet = customKey != ""
	pub.APIKeyPreview = maskSecret(customKey)
	pub.GlobalModel = globalModel
	pub.GlobalProviderName = globalCfg.ProviderName
	pub.GlobalConfigured = globalReady

	switch pub.ModelSource {
	case "provider":
		if resolved, ok := aiprovider.Resolve(ctx, h.store, pub.ProviderID, pub.Model); ok {
			pub.ProviderName = resolved.ProviderName
			pub.EffectiveProviderName = resolved.ProviderName
			pub.EffectiveModel = resolved.Model
			pub.EffectiveConfigured = resolved.IsConfigured() && (resolved.Protocol == "agent" || strings.TrimSpace(resolved.Model) != "")
		} else {
			pub.EffectiveModel = pub.Model
			pub.EffectiveConfigured = false
		}
	case "custom":
		// If a legacy custom base_url matches an existing AI provider, surface its name cleanly too.
		pub.EffectiveModel = pub.Model
		pub.EffectiveConfigured = customKey != ""
	default:
		pub.EffectiveModel = globalModel
		pub.EffectiveProviderName = globalCfg.ProviderName
		pub.EffectiveConfigured = globalReady
	}
	return pub
}

func (h *handler) saveSettings(ctx context.Context, incoming MeetingSettings) (MeetingSettings, error) {
	if h.store == nil {
		return MeetingSettings{}, errors.New("store unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Materialize legacy provider migration before mutating meetings:settings so a
	// legacy custom meeting gateway is persisted into services:ai_providers first.
	_, _ = aiprovider.LoadConfig(ctx, h.store)

	existing, err := h.loadRawSettings(ctx)
	if err != nil {
		return MeetingSettings{}, err
	}

	if strings.TrimSpace(incoming.BaseURL) == "" && strings.TrimSpace(existing.BaseURL) != "" {
		incoming.BaseURL = existing.BaseURL
	}
	next := normalizeMeetingSettings(incoming)
	if incoming.APIKeyClear {
		next.APIKey = ""
	} else if strings.TrimSpace(incoming.APIKey) != "" {
		next.APIKey = strings.TrimSpace(incoming.APIKey)
	} else {
		next.APIKey = strings.TrimSpace(existing.APIKey)
	}
	next.APIKeyClear = false
	next.APIKeySet = next.APIKey != ""
	next.APIKeyPreview = ""
	next.ProviderName = ""
	next.EffectiveModel = ""
	next.EffectiveProviderName = ""
	next.EffectiveConfigured = false
	next.GlobalModel = ""
	next.GlobalProviderName = ""
	next.GlobalConfigured = false

	if err := h.store.SetPreference(ctx, PrefMeetingsSettings, next, true); err != nil {
		return MeetingSettings{}, err
	}
	return h.decoratePublicSettings(ctx, next), nil
}

func (h *handler) loadMeetings(ctx context.Context) ([]MeetingRecord, error) {
	if h.store == nil {
		return []MeetingRecord{}, nil
	}
	pref, err := h.store.GetPreference(ctx, PrefMeetingsRecords)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []MeetingRecord{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return nil, err
	}
	var list []MeetingRecord
	if err := json.Unmarshal(raw, &list); err != nil {
		return []MeetingRecord{}, nil
	}
	for i := range list {
		if list[i].ActionItems == nil {
			list[i].ActionItems = []MeetingActionItem{}
		}
		if list[i].Topics == nil {
			list[i].Topics = []MeetingTopic{}
		}
		if list[i].Segments == nil {
			list[i].Segments = []MeetingTranscriptSegment{}
		}
		for j := range list[i].ActionItems {
			list[i].ActionItems[j].MeetingID = list[i].ID
			list[i].ActionItems[j].MeetingTitle = list[i].Title
			list[i].ActionItems[j].Status = normalizeStatus(list[i].ActionItems[j].Status)
			list[i].ActionItems[j].Priority = normalizePriority(list[i].ActionItems[j].Priority)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt != list[j].UpdatedAt {
			return list[i].UpdatedAt > list[j].UpdatedAt
		}
		return list[i].MeetingDate > list[j].MeetingDate
	})
	return list, nil
}

func (h *handler) saveMeeting(ctx context.Context, rec MeetingRecord) (MeetingRecord, error) {
	if h.store == nil {
		return MeetingRecord{}, errors.New("store unavailable")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec.Title = strings.TrimSpace(rec.Title)
	if rec.Title == "" {
		rec.Title = "会议纪要 " + time.Now().Format("2006-01-02")
	}
	rec.MeetingDate = strings.TrimSpace(rec.MeetingDate)
	if rec.MeetingDate == "" {
		rec.MeetingDate = time.Now().Format("2006-01-02")
	}
	rec.ID = strings.TrimSpace(rec.ID)
	if rec.ID == "" {
		rec.ID = randomID("mtg")
	}
	rec.Participants = cleanStringList(rec.Participants)
	rec.ProjectKey = strings.TrimSpace(rec.ProjectKey)
	rec.Workspace = strings.TrimSpace(rec.Workspace)
	rec.Decisions = cleanStringList(rec.Decisions)
	rec.Risks = cleanStringList(rec.Risks)
	rec.Topics = cleanTopics(rec.Topics)
	rec.Summary = strings.TrimSpace(rec.Summary)
	rec.MinutesMarkdown = strings.TrimSpace(rec.MinutesMarkdown)
	if rec.CreatedAt == "" {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	cleanedSegs := make([]MeetingTranscriptSegment, 0, len(rec.Segments))
	for _, seg := range rec.Segments {
		txt := strings.TrimSpace(seg.Text)
		if txt == "" {
			continue
		}
		id := strings.TrimSpace(seg.ID)
		if id == "" {
			id = randomID("seg")
		}
		kind := seg.Kind
		if kind != "note" {
			kind = "speech"
		}
		cleanedSegs = append(cleanedSegs, MeetingTranscriptSegment{
			ID:         id,
			Timestamp:  strings.TrimSpace(seg.Timestamp),
			Seconds:    seg.Seconds,
			Speaker:    strings.TrimSpace(seg.Speaker),
			Text:       txt,
			Kind:       kind,
			Bookmarked: seg.Bookmarked,
		})
	}
	rec.Segments = cleanedSegs

	cleanedItems := make([]MeetingActionItem, 0, len(rec.ActionItems))
	for _, it := range rec.ActionItems {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		id := strings.TrimSpace(it.ID)
		if id == "" {
			id = randomID("act")
		}
		created := strings.TrimSpace(it.CreatedAt)
		if created == "" {
			created = now
		}
		cleanedItems = append(cleanedItems, MeetingActionItem{
			ID:           id,
			MeetingID:    rec.ID,
			MeetingTitle: rec.Title,
			Text:         text,
			Owner:        strings.TrimSpace(it.Owner),
			DueDate:      strings.TrimSpace(it.DueDate),
			Priority:     normalizePriority(it.Priority),
			Status:       normalizeStatus(it.Status),
			CreatedAt:    created,
			UpdatedAt:    now,
		})
	}
	rec.ActionItems = cleanedItems

	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.loadMeetings(ctx)
	if err != nil {
		return MeetingRecord{}, err
	}
	replaced := false
	for i := range list {
		if list[i].ID == rec.ID {
			if list[i].CreatedAt != "" {
				rec.CreatedAt = list[i].CreatedAt
			}
			list[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		list = append([]MeetingRecord{rec}, list...)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt > list[j].UpdatedAt
	})
	if len(list) > maxStoredMeetings {
		list = list[:maxStoredMeetings]
	}
	if err := h.store.SetPreference(ctx, PrefMeetingsRecords, list, true); err != nil {
		return MeetingRecord{}, err
	}
	return rec, nil
}

func (h *handler) deleteMeeting(ctx context.Context, id string) error {
	if h.store == nil {
		return errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("缺少会议记录 id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.loadMeetings(ctx)
	if err != nil {
		return err
	}
	next := make([]MeetingRecord, 0, len(list))
	for _, rec := range list {
		if rec.ID != id {
			next = append(next, rec)
		}
	}
	return h.store.SetPreference(ctx, PrefMeetingsRecords, next, true)
}

func (h *handler) listActionItems(ctx context.Context, statusFilter string) ([]MeetingActionItem, int, error) {
	list, err := h.loadMeetings(ctx)
	if err != nil {
		return nil, 0, err
	}
	filter := strings.ToLower(strings.TrimSpace(statusFilter))
	items := make([]MeetingActionItem, 0)
	openCount := 0
	for _, m := range list {
		for _, it := range m.ActionItems {
			st := normalizeStatus(it.Status)
			if st == "open" {
				openCount++
			}
			if filter == "open" && st != "open" {
				continue
			}
			if filter == "done" && st != "done" {
				continue
			}
			it.MeetingID = m.ID
			it.MeetingTitle = m.Title
			it.Status = st
			items = append(items, it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Status != items[j].Status {
			return items[i].Status == "open"
		}
		if items[i].DueDate != items[j].DueDate {
			if items[i].DueDate == "" {
				return false
			}
			if items[j].DueDate == "" {
				return true
			}
			return items[i].DueDate < items[j].DueDate
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, openCount, nil
}

func (h *handler) patchActionItem(ctx context.Context, req ActionItemPatchRequest) (MeetingActionItem, error) {
	if h.store == nil {
		return MeetingActionItem{}, errors.New("store unavailable")
	}
	meetingID := strings.TrimSpace(req.MeetingID)
	itemID := strings.TrimSpace(req.ItemID)
	if itemID == "" {
		return MeetingActionItem{}, errors.New("缺少行动项 item_id")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.loadMeetings(ctx)
	if err != nil {
		return MeetingActionItem{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for mi := range list {
		if meetingID != "" && list[mi].ID != meetingID {
			continue
		}
		for ai := range list[mi].ActionItems {
			if list[mi].ActionItems[ai].ID != itemID {
				continue
			}
			it := &list[mi].ActionItems[ai]
			if req.Status != nil {
				it.Status = normalizeStatus(*req.Status)
			}
			if req.Text != nil && strings.TrimSpace(*req.Text) != "" {
				it.Text = strings.TrimSpace(*req.Text)
			}
			if req.Owner != nil {
				it.Owner = strings.TrimSpace(*req.Owner)
			}
			if req.DueDate != nil {
				it.DueDate = strings.TrimSpace(*req.DueDate)
			}
			if req.Priority != nil {
				it.Priority = normalizePriority(*req.Priority)
			}
			it.MeetingID = list[mi].ID
			it.MeetingTitle = list[mi].Title
			it.UpdatedAt = now
			list[mi].UpdatedAt = now
			updated := *it
			if err := h.store.SetPreference(ctx, PrefMeetingsRecords, list, true); err != nil {
				return MeetingActionItem{}, err
			}
			return updated, nil
		}
	}
	return MeetingActionItem{}, errors.New("未找到对应行动项")
}
