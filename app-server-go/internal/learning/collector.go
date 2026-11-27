package learning

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

var frictionMarkers = []string{
	"为什么", "怎么会", "原理", "排查", "报错", "失败", "冲突", "卡住",
	"不生效", "怎么解决", "啥意思", "解释", "区别", "机制", "why ", "error", "fail",
}

// CollectEvidencePack gathers bounded, deterministic multi-channel local signals
// for the Mentor Bot to inspect deeply with its native tools.
func (s *Service) CollectEvidencePack(
	ctx context.Context,
	runID string,
	trigger string,
	lookbackDays int,
	filterProjectKeys []string,
	cfg ScheduleConfig,
	mentorBotWorkspace string,
) (*EvidencePack, error) {
	if lookbackDays <= 0 {
		lookbackDays = DefaultLookbackDays
	}
	toTime := s.now().UTC()
	fromTime := toTime.AddDate(0, 0, -lookbackDays)

	pack := &EvidencePack{
		RunID:          runID,
		Trigger:        trigger,
		From:           fromTime.Format(time.RFC3339),
		To:             toTime.Format(time.RFC3339),
		LookbackDays:   lookbackDays,
		GeneratedAt:    toTime.Format(time.RFC3339),
		Projects:       []ProjectEvidencePack{},
		ExcludedTopics: []ExcludedTopicRef{},
		ActiveTopics:   []ExcludedTopicRef{},
	}

	// Inject mastery & active topic references so Mentor Bot avoids repeating known topics.
	if existingTopics, err := s.repo.ListTopics(ctx); err == nil {
		for _, t := range existingTopics {
			ref := ExcludedTopicRef{
				ID:          t.ID,
				Fingerprint: t.Fingerprint,
				Title:       t.Title,
				Status:      t.Status,
				ProjectName: t.ProjectName,
			}
			if t.Status == "mastered" || t.Status == "dismissed" || t.Status == "snoozed" {
				pack.ExcludedTopics = append(pack.ExcludedTopics, ref)
			} else {
				pack.ActiveTopics = append(pack.ActiveTopics, ref)
			}
		}
	}

	if s.options.Store == nil {
		return pack, nil
	}

	mentorWSKey := normWorkspaceKey(mentorBotWorkspace)
	excludedPatterns := make([]string, 0, len(cfg.ExcludedWorkspaces))
	for _, ex := range cfg.ExcludedWorkspaces {
		if norm := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(ex, "\\", "/"))); norm != "" {
			excludedPatterns = append(excludedPatterns, norm)
		}
	}
	isExcluded := func(ws, name string) bool {
		normWS := normWorkspaceKey(ws)
		if mentorWSKey != "" && normWS != "" && (normWS == mentorWSKey || strings.HasPrefix(normWS, mentorWSKey+"/")) {
			return true
		}
		normName := strings.ToLower(strings.TrimSpace(name))
		for _, pat := range excludedPatterns {
			if (normWS != "" && strings.Contains(normWS, pat)) || (normName != "" && strings.Contains(normName, pat)) {
				return true
			}
		}
		return false
	}

	filterSet := make(map[string]bool, len(filterProjectKeys))
	for _, k := range filterProjectKeys {
		if norm := strings.ToLower(strings.TrimSpace(k)); norm != "" {
			filterSet[norm] = true
		}
	}
	projectAllowed := func(k string) bool {
		if len(filterSet) == 0 {
			return true
		}
		return filterSet[strings.ToLower(strings.TrimSpace(k))]
	}

	buckets := make(map[string]*ProjectEvidencePack)
	ensureBucket := func(ws, name, connID string) *ProjectEvidencePack {
		key, display := normalizeProjectKey(ws, name)
		if !projectAllowed(key) || isExcluded(ws, display) {
			return nil
		}
		if b, ok := buckets[key]; ok {
			if b.Workspace == "" && ws != "" {
				b.Workspace = ws
			}
			if b.ConnectionID == "" && connID != "" {
				b.ConnectionID = connID
			}
			return b
		}
		b := &ProjectEvidencePack{
			ProjectKey:   key,
			ProjectName:  display,
			Workspace:    strings.TrimSpace(ws),
			ConnectionID: strings.TrimSpace(connID),
		}
		buckets[key] = b
		return b
	}

	type sessRow struct {
		agentID      string
		sessionID    string
		title        string
		workspace    string
		projectName  string
		connectionID string
		updatedAt    time.Time
	}
	var inRangeSessions []sessRow
	sessionByID := make(map[string]sessRow)

	if sessions, err := s.options.Store.ListSessions(ctx, ""); err == nil {
		for _, sMap := range sessions {
			sid := strVal(sMap["id"])
			aid := strVal(sMap["agent_id"])
			if sid == "" || aid == "" {
				continue
			}
			if eph, _ := sMap["ephemeral"].(bool); eph {
				continue
			}
			nativeKind := strings.ToLower(strings.TrimSpace(strVal(sMap["native_kind"])))
			if nativeKind == "subagent" || nativeKind == "smoke" {
				continue
			}
			ws := strVal(sMap["worktree_root_workspace"])
			if ws == "" {
				ws = strVal(sMap["workspace"])
			}
			pName := strVal(sMap["project_name"])
			connID := strVal(sMap["connection_id"])
			if isExcluded(ws, pName) {
				continue
			}
			upd, ok := parseAnyTime(sMap["updated_at"])
			row := sessRow{
				agentID:      aid,
				sessionID:    sid,
				title:        strings.TrimSpace(strVal(sMap["title"])),
				workspace:    ws,
				projectName:  pName,
				connectionID: connID,
				updatedAt:    upd,
			}
			sessionByID[sid] = row
			if ok && !upd.Before(fromTime) && !upd.After(toTime) {
				if ensureBucket(ws, pName, connID) != nil {
					inRangeSessions = append(inRangeSessions, row)
				}
			}
		}
	}

	sort.Slice(inRangeSessions, func(i, j int) bool {
		return inRangeSessions[i].updatedAt.After(inRangeSessions[j].updatedAt)
	})
	pack.ActiveSessions = len(inRangeSessions)

	// Channel 1: Cross-Agent Sessions & Friction Snippets
	if cfg.SignalSources.Sessions && len(inRangeSessions) > 0 {
		maxSessions := len(inRangeSessions)
		if maxSessions > 25 {
			maxSessions = 25
		}
		clues := make([]*SessionClue, maxSessions)
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i := 0; i < maxSessions; i++ {
			i := i
			row := inRangeSessions[i]
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				var rawRows []map[string]any
				if s.options.HistoryReader != nil && i < 15 {
					rCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
					nativeRows, err := s.options.HistoryReader(rCtx, row.agentID, row.sessionID, 30)
					cancel()
					if err == nil && len(nativeRows) > 0 {
						rawRows = nativeRows
					}
				}
				if len(rawRows) == 0 {
					dbRows, err := s.options.Store.QueryRows(ctx, `
						SELECT role, kind, text, created_at
						FROM messages
						WHERE agent_id = ? AND session_id = ?
						  AND role IN ('user', 'assistant')
						  AND kind NOT IN ('tool', 'thinking', 'thought', 'status', 'event', 'system', 'activity')
						ORDER BY created_at DESC, row_id DESC
						LIMIT 30`, row.agentID, row.sessionID)
					if err == nil {
						rawRows = dbRows
					}
				}

				var questions []string
				var snippets []string
				for _, m := range rawRows {
					role := strings.ToLower(strVal(m["role"]))
					text := collapseSpaces(strVal(m["text"]))
					if text == "" {
						continue
					}
					if role == "user" {
						if looksLikeFrictionQuestion(text) && len(questions) < 4 {
							questions = append(questions, truncateText(text, 160))
						} else if len(snippets) < 2 {
							snippets = append(snippets, "用户: "+truncateText(text, 140))
						}
					} else if role == "assistant" && len(snippets) < 3 {
						snippets = append(snippets, "回复: "+truncateText(text, 140))
					}
				}

				title := store.DisplaySessionTitle(row.title, row.sessionID, row.workspace, row.agentID)
				if title == "" {
					title = "会话 " + truncateText(row.sessionID, 12)
				}
				clues[i] = &SessionClue{
					AgentID:        row.agentID,
					SessionID:      row.sessionID,
					SessionKey:     row.agentID + "::" + row.sessionID,
					Title:          title,
					UpdatedAt:      row.updatedAt.Format(time.RFC3339),
					MessageCount:   len(rawRows),
					UserQuestions:  questions,
					RecentSnippets: snippets,
				}
			}()
		}
		wg.Wait()
		for i, clue := range clues {
			if clue == nil {
				continue
			}
			row := inRangeSessions[i]
			if b := ensureBucket(row.workspace, row.projectName, row.connectionID); b != nil {
				b.Sessions = append(b.Sessions, *clue)
				pack.EvidenceCount++
			}
		}
	}

	// Channel 2: Git Checkpoints & Local Git Commit Stats (AI Black-Box Code Debt)
	if cfg.SignalSources.Git {
		if rows, err := s.options.Store.QueryRows(ctx, `
			SELECT agent_id, session_id, label, workspace, repo_root, branch,
			       files_changed, insertions, deletions, created_at
			FROM session_checkpoints
			ORDER BY created_at DESC
			LIMIT 200`); err == nil {
			for _, r := range rows {
				ts, ok := parseAnyTime(r["created_at"])
				if !ok || ts.Before(fromTime) || ts.After(toTime) {
					continue
				}
				ws := strVal(r["repo_root"])
				if ws == "" {
					ws = strVal(r["workspace"])
				}
				sess := sessionByID[strVal(r["session_id"])]
				b := ensureBucket(ws, sess.projectName, sess.connectionID)
				if b == nil || len(b.GitChanges) >= 12 {
					continue
				}
				b.GitChanges = append(b.GitChanges, GitChangeClue{
					Kind:         "checkpoint",
					Timestamp:    ts.Format(time.RFC3339),
					Branch:       strVal(r["branch"]),
					Message:      nonEmpty(strVal(r["label"]), "工作区代码检查点"),
					FilesChanged: intVal(r["files_changed"]),
					Insertions:   intVal(r["insertions"]),
					Deletions:    intVal(r["deletions"]),
				})
				pack.EvidenceCount++
			}
		}

		// Inspect local git repos discovered in buckets (bounded, read-only, 3s timeout per repo).
		for _, b := range buckets {
			if b.Workspace == "" || (b.ConnectionID != "" && b.ConnectionID != "local") {
				continue
			}
			if _, statErr := os.Stat(b.Workspace); statErr != nil {
				continue
			}
			commits := collectLocalGitCommits(ctx, b.Workspace, fromTime, 10)
			for _, c := range commits {
				b.GitChanges = append(b.GitChanges, c)
				pack.CommitCount++
				pack.EvidenceCount++
			}
		}
	}

	// Channel 3: Failed Tasks & Optional External Plugin Failure Signals
	if cfg.SignalSources.Failures {
		if rows, err := s.options.Store.QueryRows(ctx, `
			SELECT id, agent_id, session_id, kind, title, status, updated_at
			FROM tasks
			WHERE status IN ('failed', 'error', 'cancelled')
			ORDER BY updated_at DESC, row_id DESC
			LIMIT 80`); err == nil {
			for _, r := range rows {
				ts, ok := parseAnyTime(r["updated_at"])
				if !ok || ts.Before(fromTime) || ts.After(toTime) {
					continue
				}
				sess := sessionByID[strVal(r["session_id"])]
				b := ensureBucket(sess.workspace, sess.projectName, sess.connectionID)
				if b == nil || len(b.Failures) >= 8 {
					continue
				}
				title := truncateText(strVal(r["title"]), 120)
				if title == "" {
					continue
				}
				b.Failures = append(b.Failures, FailureClue{
					Kind:      "task_failed",
					Title:     title,
					Detail:    fmt.Sprintf("任务状态: %s (%s)", strVal(r["status"]), strVal(r["kind"])),
					Timestamp: ts.Format(time.RFC3339),
				})
				pack.EvidenceCount++
			}
		}

		if s.options.FailureSignals != nil {
			for _, b := range buckets {
				if b.Workspace == "" || len(b.Failures) >= 10 {
					continue
				}
				gCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				signals := s.options.FailureSignals.CollectFailureSignals(gCtx, b.Workspace, b.ConnectionID, "", 8)
				cancel()
				for _, sig := range signals {
					if len(b.Failures) >= 10 {
						break
					}
					if strings.TrimSpace(sig.Timestamp) != "" {
						if ts, ok := parseAnyTime(sig.Timestamp); ok && ts.Before(fromTime) {
							continue
						}
					}
					title := strings.TrimSpace(sig.Title)
					if title == "" {
						continue
					}
					kind := strings.TrimSpace(sig.Source)
					if kind == "" {
						kind = "external"
					}
					b.Failures = append(b.Failures, FailureClue{
						Kind:      kind,
						Title:     title,
						Detail:    strings.TrimSpace(sig.Detail),
						Timestamp: strings.TrimSpace(sig.Timestamp),
					})
					pack.EvidenceCount++
				}
			}
		}
	}

	// Channel 4: Meetings & Office Docs
	if cfg.SignalSources.Meetings {
		if pref, err := s.options.Store.GetPreference(ctx, "meetings:records:v1"); err == nil && pref.Exists && pref.Value != nil {
			if rawItems := extractSliceMap(pref.Value); len(rawItems) > 0 {
				b := ensureBucket("", "会议与办公文档", "")
				if b != nil {
					for _, m := range rawItems {
						ts, ok := parseAnyTime(nonEmpty(strVal(m["updated_at"]), strVal(m["created_at"])))
						if ok && (ts.Before(fromTime) || ts.After(toTime)) {
							continue
						}
						title := strings.TrimSpace(strVal(m["title"]))
						if title == "" {
							continue
						}
						summary := truncateText(collapseSpaces(strVal(m["summary"])), 200)
						b.MeetingsDocs = append(b.MeetingsDocs, MeetingDocClue{
							Kind:      "meeting",
							Title:     title,
							Summary:   summary,
							Timestamp: nonEmpty(strVal(m["updated_at"]), strVal(m["created_at"])),
						})
						pack.EvidenceCount++
						if len(b.MeetingsDocs) >= 8 {
							break
						}
					}
				}
			}
		}
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := buckets[k]
		if len(b.Sessions) == 0 && len(b.GitChanges) == 0 && len(b.Failures) == 0 && len(b.MeetingsDocs) == 0 {
			continue
		}
		pack.Projects = append(pack.Projects, *b)
	}

	return pack, nil
}

func collectLocalGitCommits(ctx context.Context, workspace string, since time.Time, limit int) []GitChangeClue {
	gCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		gCtx,
		"git", "-C", workspace, "log",
		"--since="+since.Format(time.RFC3339),
		"--no-merges",
		fmt.Sprintf("-n%d", limit),
		"--pretty=format:@@@%H|%aI|%s",
		"--name-only",
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil
	}
	var out []GitChangeClue
	blocks := strings.Split(stdout.String(), "@@@")
	for _, blk := range blocks {
		blk = strings.TrimSpace(blk)
		if blk == "" {
			continue
		}
		lines := strings.Split(blk, "\n")
		header := strings.SplitN(lines[0], "|", 3)
		if len(header) < 3 {
			continue
		}
		files := make([]string, 0, len(lines)-1)
		for _, ln := range lines[1:] {
			f := strings.TrimSpace(ln)
			if f != "" && len(files) < 12 {
				files = append(files, f)
			}
		}
		sha := strings.TrimSpace(header[0])
		if len(sha) > 10 {
			sha = sha[:10]
		}
		out = append(out, GitChangeClue{
			Kind:         "commit",
			CommitSHA:    sha,
			Timestamp:    strings.TrimSpace(header[1]),
			Message:      truncateText(strings.TrimSpace(header[2]), 140),
			FilesChanged: len(files),
			Files:        files,
		})
	}
	return out
}

func looksLikeFrictionQuestion(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(text, "?") || strings.Contains(text, "？") {
		return true
	}
	for _, marker := range frictionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func normalizeProjectKey(workspace, projectName string) (string, string) {
	ws := strings.TrimSpace(workspace)
	name := strings.TrimSpace(projectName)
	if ws != "" {
		norm := strings.TrimRight(strings.ReplaceAll(ws, "\\", "/"), "/")
		if name == "" {
			base := path.Base(norm)
			if base != "" && base != "." && base != "/" {
				name = base
			} else {
				name = norm
			}
		}
		return strings.ToLower(norm), name
	}
	if name != "" {
		return "name:" + strings.ToLower(name), name
	}
	return "general", "通用工作区"
}

func normWorkspaceKey(ws string) string {
	norm := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(ws), "\\", "/"), "/")
	return strings.ToLower(norm)
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}

func intVal(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func nonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateText(s string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}

func parseAnyTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func extractSliceMap(val any) []map[string]any {
	if m, ok := val.(map[string]any); ok {
		if items, exists := m["items"]; exists {
			val = items
		}
	}
	rawSlice, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rawSlice))
	for _, item := range rawSlice {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}
