package digest

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

// CollectRequest specifies the time window, project scope, and data sources to gather.
type CollectRequest struct {
	From               string   `json:"from"`
	To                 string   `json:"to"`
	ProjectKeys        []string `json:"project_keys,omitempty"`
	IncludeSessions    *bool    `json:"include_sessions,omitempty"`
	IncludeGit         *bool    `json:"include_git,omitempty"`
	IncludeTasks       *bool    `json:"include_tasks,omitempty"`
	IncludeCheckpoints *bool    `json:"include_checkpoints,omitempty"`
	IncludeSQLAudit    *bool    `json:"include_sql_audit,omitempty"`
	IncludeMeetings    *bool    `json:"include_meetings,omitempty"`
	IncludeOfficeDocs  *bool    `json:"include_office_docs,omitempty"`
	LocalOnly          *bool    `json:"local_only,omitempty"`
}

// DigestItem represents a single selectable evidence item collected from Astrorder.
type DigestItem struct {
	ItemID      string         `json:"item_id"`
	Kind        string         `json:"kind"` // session, git_commit, task, checkpoint, sql_audit, meeting, office_doc
	ProjectKey  string         `json:"project_key"`
	ProjectName string         `json:"project_name"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Timestamp   string         `json:"timestamp"`
	Meta        map[string]any `json:"meta,omitempty"`
	Selected    bool           `json:"selected"`
}

// ProjectOption describes an available project that can be filtered in the UI.
type ProjectOption struct {
	ProjectKey  string `json:"project_key"`
	ProjectName string `json:"project_name"`
	Workspace   string `json:"workspace,omitempty"`
}

// DigestProjectGroup aggregates all collected evidence items for one project.
type DigestProjectGroup struct {
	ProjectKey     string       `json:"project_key"`
	ProjectName    string       `json:"project_name"`
	Workspace      string       `json:"workspace,omitempty"`
	SessionCount   int          `json:"session_count"`
	CommitCount    int          `json:"commit_count"`
	TaskCount      int          `json:"task_count"`
	SQLAuditCount  int          `json:"sql_audit_count"`
	MeetingCount   int          `json:"meeting_count"`
	OfficeDocCount int          `json:"office_doc_count"`
	FilesChanged   int          `json:"files_changed"`
	Insertions     int          `json:"insertions"`
	Deletions      int          `json:"deletions"`
	Items          []DigestItem `json:"items"`
}

// DigestSummaryStats holds top-level KPI counts for the collected bundle.
type DigestSummaryStats struct {
	ProjectCount    int `json:"project_count"`
	SessionCount    int `json:"session_count"`
	CommitCount     int `json:"commit_count"`
	TaskCount       int `json:"task_count"`
	CheckpointCount int `json:"checkpoint_count"`
	SQLAuditCount   int `json:"sql_audit_count"`
	MeetingCount    int `json:"meeting_count"`
	OfficeDocCount  int `json:"office_doc_count"`
	FilesChanged    int `json:"files_changed"`
	Insertions      int `json:"insertions"`
	Deletions       int `json:"deletions"`
}

// DigestBundle is the response payload of POST /api/v1/digest/collect.
type DigestBundle struct {
	From              string               `json:"from"`
	To                string               `json:"to"`
	GeneratedAt       string               `json:"generated_at"`
	LLMConfigured     bool                 `json:"llm_configured"`
	LLMModel          string               `json:"llm_model,omitempty"`
	Projects          []DigestProjectGroup `json:"projects"`
	AvailableProjects []ProjectOption      `json:"available_projects"`
	Stats             DigestSummaryStats   `json:"stats"`
}

type projectBucket struct {
	key        string
	name       string
	workspace  string
	remote     bool
	group      *DigestProjectGroup
	seenItems  map[string]bool
}

func boolOrDefault(ptr *bool, def bool) bool {
	if ptr == nil {
		return def
	}
	return *ptr
}

func parseTimeRange(fromRaw, toRaw string) (time.Time, time.Time) {
	now := time.Now()
	toTime := now
	if parsed, ok := parseFlexTimeString(toRaw, true); ok {
		toTime = parsed
	}
	fromTime := toTime.AddDate(0, 0, -7)
	if parsed, ok := parseFlexTimeString(fromRaw, false); ok {
		fromTime = parsed
	}
	if fromTime.After(toTime) {
		fromTime, toTime = toTime, fromTime
	}
	return fromTime.UTC(), toTime.UTC()
}

func parseFlexTimeString(raw string, endOfDay bool) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	if len(s) == 10 {
		if d, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			if endOfDay {
				return d.Add(24*time.Hour - time.Nanosecond), true
			}
			return d, true
		}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseFlexTimeAny(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case string:
		p, ok := parseFlexTimeString(t, false)
		return p.UTC(), ok
	case []byte:
		p, ok := parseFlexTimeString(string(t), false)
		return p.UTC(), ok
	default:
		return time.Time{}, false
	}
}

func normalizeProjectKey(workspace, projectName, projectID string) (string, string) {
	ws := strings.TrimSpace(workspace)
	name := strings.TrimSpace(projectName)
	if ws != "" {
		norm := strings.ReplaceAll(ws, "\\", "/")
		norm = strings.TrimRight(norm, "/")
		if len(norm) >= 2 && norm[1] == ':' {
			norm = strings.ToLower(norm[:2]) + norm[2:]
		}
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
	if pid := strings.TrimSpace(projectID); pid != "" {
		return "id:" + strings.ToLower(pid), pid
	}
	return "general", "通用工作区"
}

func stableItemID(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// Collect gathers real multi-source work evidence across sessions, tasks,
// checkpoints, local git logs, and DBX SQL audit logs within the requested time window.
func (h *handler) Collect(ctx context.Context, req CollectRequest) (*DigestBundle, error) {
	fromTime, toTime := parseTimeRange(req.From, req.To)
	incSessions := boolOrDefault(req.IncludeSessions, true)
	incGit := boolOrDefault(req.IncludeGit, true)
	incTasks := boolOrDefault(req.IncludeTasks, false)
	incCheckpoints := boolOrDefault(req.IncludeCheckpoints, true)
	incSQLAudit := boolOrDefault(req.IncludeSQLAudit, true)
	incMeetings := boolOrDefault(req.IncludeMeetings, true)
	incOfficeDocs := boolOrDefault(req.IncludeOfficeDocs, true)
	localOnly := boolOrDefault(req.LocalOnly, false)

	filterProjects := make(map[string]bool, len(req.ProjectKeys))
	for _, k := range req.ProjectKeys {
		trimmed := strings.ToLower(strings.TrimSpace(k))
		if trimmed != "" {
			filterProjects[trimmed] = true
		}
	}
	projectAllowed := func(key string) bool {
		if len(filterProjects) == 0 {
			return true
		}
		return filterProjects[strings.ToLower(key)]
	}

	buckets := make(map[string]*projectBucket)
	availableMap := make(map[string]ProjectOption)

	ensureBucket := func(key, name, workspace string, remote bool) *projectBucket {
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "" {
			k = "general"
		}
		if b, exists := buckets[k]; exists {
			if b.workspace == "" && workspace != "" {
				b.workspace = workspace
				b.group.Workspace = workspace
			}
			if (b.name == "" || b.name == "通用工作区") && name != "" {
				b.name = name
				b.group.ProjectName = name
			}
			return b
		}
		if name == "" {
			name = "通用工作区"
		}
		b := &projectBucket{
			key:       k,
			name:      name,
			workspace: workspace,
			remote:    remote,
			seenItems: make(map[string]bool),
			group: &DigestProjectGroup{
				ProjectKey:  k,
				ProjectName: name,
				Workspace:   workspace,
				Items:       make([]DigestItem, 0),
			},
		}
		buckets[k] = b
		return b
	}

	registerAvailable := func(key, name, workspace string) {
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "" {
			return
		}
		if cur, ok := availableMap[k]; ok {
			if cur.Workspace == "" && workspace != "" {
				cur.Workspace = workspace
				availableMap[k] = cur
			}
			return
		}
		availableMap[k] = ProjectOption{
			ProjectKey:  k,
			ProjectName: name,
			Workspace:   workspace,
		}
	}

	addItem := func(b *projectBucket, item DigestItem) bool {
		if b.seenItems[item.ItemID] {
			return false
		}
		b.seenItems[item.ItemID] = true
		item.ProjectKey = b.key
		item.ProjectName = b.name
		item.Selected = true
		b.group.Items = append(b.group.Items, item)
		return true
	}

	// Build session index for correlating messages, tasks, checkpoints, and SQL audit records
	type sessionMeta struct {
		agentID      string
		sessionID    string
		title        string
		workspace    string
		projectKey   string
		projectName  string
		connectionID string
		nativeKind   string
		updatedAt    time.Time
		ephemeral    bool
		inTimeRange  bool
	}
	sessionsByKey := make(map[string]sessionMeta)
	sessionsByID := make(map[string]sessionMeta)
	inRangeSessions := make([]sessionMeta, 0)

	if h.store != nil {
		if projects, err := h.store.ListProjects(ctx); err == nil {
			for _, p := range projects {
				ws := stringField(p["workspace"])
				pName := stringField(p["project_name"])
				pID := stringField(p["project_id"])
				connID := stringField(p["connection_id"])
				isRemote := connID != "" && connID != "local"
				if localOnly && isRemote {
					continue
				}
				key, display := normalizeProjectKey(ws, pName, pID)
				registerAvailable(key, display, ws)
				if projectAllowed(key) {
					ensureBucket(key, display, ws, isRemote)
				}
			}
		}

		if sessions, err := h.store.ListSessions(ctx, ""); err == nil {
			for _, s := range sessions {
				agentID := stringField(s["agent_id"])
				sessID := stringField(s["id"])
				if sessID == "" {
					continue
				}
				ws := stringField(s["worktree_root_workspace"])
				if ws == "" {
					ws = stringField(s["workspace"])
				}
				pName := stringField(s["project_name"])
				pID := stringField(s["project_id"])
				connID := stringField(s["connection_id"])
				isRemote := connID != "" && connID != "local"
				if localOnly && isRemote {
					continue
				}
				nativeKind := strings.ToLower(strings.TrimSpace(stringField(s["native_kind"])))
				key, display := normalizeProjectKey(ws, pName, pID)
				upd, hasUpd := parseFlexTimeAny(s["updated_at"])
				eph, _ := s["ephemeral"].(bool)
				inRange := !eph &&
					nativeKind != "subagent" &&
					nativeKind != "smoke" &&
					hasUpd &&
					!upd.Before(fromTime) &&
					!upd.After(toTime)
				meta := sessionMeta{
					agentID:      agentID,
					sessionID:    sessID,
					title:        strings.TrimSpace(stringField(s["title"])),
					workspace:    ws,
					projectKey:   key,
					projectName:  display,
					connectionID: connID,
					nativeKind:   nativeKind,
					updatedAt:    upd,
					ephemeral:    eph,
					inTimeRange:  inRange,
				}
				sessionsByKey[agentID+"\x00"+sessID] = meta
				if _, exists := sessionsByID[sessID]; !exists {
					sessionsByID[sessID] = meta
				}
				registerAvailable(key, display, ws)
				if inRange && projectAllowed(key) {
					inRangeSessions = append(inRangeSessions, meta)
				}
			}
		}

		// 1. Sessions & conversation conclusions: strictly filtered by session time range FIRST.
		// Reads live native history (Antigravity / Codex / Hermes) concurrently with bounded
		// concurrency and falls back to persisted SQLite messages when unavailable.
		if incSessions && len(inRangeSessions) > 0 {
			sort.Slice(inRangeSessions, func(i, j int) bool {
				return inRangeSessions[i].updatedAt.After(inRangeSessions[j].updatedAt)
			})
			type msgSnippet struct {
				role string
				text string
				at   time.Time
			}
			type fetchedSession struct {
				meta      sessionMeta
				msgs      []msgSnippet
				skipStale bool
			}

			results := make([]fetchedSession, len(inRangeSessions))
			sem := make(chan struct{}, 4)
			var wg sync.WaitGroup
			for idx, meta := range inRangeSessions {
				idx := idx
				meta := meta
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					var rawRows []map[string]any
					// Prefer live native history for the top 30 most recent in-range sessions
					if h.historyReader != nil && idx < 30 {
						readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
						nativeRows, err := h.historyReader(readCtx, meta.agentID, meta.sessionID, 40)
						cancel()
						if err == nil && len(nativeRows) > 0 {
							rawRows = nativeRows
						}
					}
					if len(rawRows) == 0 {
						dbRows, err := h.store.QueryRows(ctx, `
							SELECT role, kind, text, created_at
							FROM messages
							WHERE agent_id = ? AND session_id = ?
							  AND role IN ('user', 'assistant')
							  AND kind NOT IN ('tool', 'thinking', 'thought', 'status', 'event', 'system', 'activity')
							ORDER BY created_at ASC, row_id ASC`, meta.agentID, meta.sessionID)
						if err == nil {
							rawRows = dbRows
						}
					}

					totalTimed := 0
					msgs := make([]msgSnippet, 0, len(rawRows))
					for _, r := range rawRows {
						role := strings.ToLower(strings.TrimSpace(stringField(r["role"])))
						if role != "user" && role != "assistant" {
							continue
						}
						kind := strings.ToLower(strings.TrimSpace(stringField(r["kind"])))
						switch kind {
						case "tool", "thinking", "thought", "status", "event", "system", "activity":
							continue
						}
						text := strings.TrimSpace(stringField(r["text"]))
						if text == "" {
							continue
						}
						ts, ok := parseFlexTimeAny(r["created_at"])
						hasRealTS := ok && ts.Year() > 1971
						if hasRealTS {
							totalTimed++
							if ts.Before(fromTime) || ts.After(toTime) {
								continue
							}
						} else {
							ts = meta.updatedAt
						}
						msgs = append(msgs, msgSnippet{
							role: role,
							text: text,
							at:   ts,
						})
					}

					results[idx] = fetchedSession{
						meta:      meta,
						msgs:      msgs,
						skipStale: totalTimed > 0 && len(msgs) == 0,
					}
				}()
			}
			wg.Wait()

			for _, res := range results {
				if res.skipStale {
					continue
				}
				meta := res.meta
				msgs := res.msgs
				b := ensureBucket(meta.projectKey, meta.projectName, meta.workspace, meta.connectionID != "" && meta.connectionID != "local")

				var userGoals []string
				var assistantNotes []string
				lastTS := meta.updatedAt
				for _, m := range msgs {
					if m.at.After(lastTS) {
						lastTS = m.at
					}
					if m.role == "user" {
						cleaned := compactSnippet(m.text, 120)
						if cleaned != "" && len(userGoals) < 3 {
							userGoals = append(userGoals, cleaned)
						}
					} else if m.role == "assistant" {
						cleaned := compactSnippet(stripLargeCodeFences(m.text), 260)
						if cleaned != "" {
							assistantNotes = append(assistantNotes, cleaned)
						}
					}
				}
				if len(assistantNotes) > 2 {
					assistantNotes = assistantNotes[len(assistantNotes)-2:]
				}

				title := store.DisplaySessionTitle(meta.title, meta.sessionID, meta.workspace, meta.agentID)
				if title == "" && len(userGoals) > 0 {
					title = truncateRunesClean(userGoals[0], 48)
				}
				if title == "" {
					title = "会话 " + truncateRunesClean(meta.sessionID, 12)
				}

				var summaryParts []string
				if len(userGoals) > 0 {
					summaryParts = append(summaryParts, "需求/指令："+strings.Join(userGoals, "；"))
				}
				if len(assistantNotes) > 0 {
					summaryParts = append(summaryParts, "关键结论："+strings.Join(assistantNotes, " | "))
				}

				item := DigestItem{
					ItemID:    stableItemID("session", meta.agentID+"\x00"+meta.sessionID),
					Kind:      "session",
					Title:     title,
					Summary:   strings.Join(summaryParts, "\n"),
					Timestamp: lastTS.Format(time.RFC3339),
					Meta: map[string]any{
						"agent_id":      meta.agentID,
						"session_id":    meta.sessionID,
						"message_count": len(msgs),
					},
				}
				if addItem(b, item) {
					b.group.SessionCount++
				}
			}
		}

		// 2. Tasks in [fromTime, toTime] (disabled by default; excludes internal tool/subagent rows)
		if incTasks {
			rows, err := h.store.QueryRows(ctx, `
				SELECT id, agent_id, session_id, kind, title, status, updated_at
				FROM tasks
				WHERE kind NOT IN ('tool', 'subagent', 'background', 'command')
				ORDER BY updated_at DESC, row_id DESC
				LIMIT 500`)
			if err == nil {
				perProjectTasks := make(map[string]int)
				for _, r := range rows {
					ts, ok := parseFlexTimeAny(r["updated_at"])
					if !ok || ts.Before(fromTime) || ts.After(toTime) {
						continue
					}
					title := strings.TrimSpace(stringField(r["title"]))
					if title == "" {
						continue
					}
					agentID := stringField(r["agent_id"])
					sessID := stringField(r["session_id"])
					meta, found := sessionsByKey[agentID+"\x00"+sessID]
					if !found {
						meta, found = sessionsByID[sessID]
					}
					if found && !meta.inTimeRange {
						continue
					}
					pKey := meta.projectKey
					pName := meta.projectName
					if pKey == "" {
						pKey, pName = "general", "通用工作区"
					}
					if !projectAllowed(pKey) || perProjectTasks[pKey] >= 25 {
						continue
					}
					b := ensureBucket(pKey, pName, meta.workspace, meta.connectionID != "" && meta.connectionID != "local")
					status := strings.TrimSpace(stringField(r["status"]))
					kind := strings.TrimSpace(stringField(r["kind"]))
					item := DigestItem{
						ItemID:    stableItemID("task", agentID, sessID, stringField(r["id"])),
						Kind:      "task",
						Title:     truncateRunesClean(title, 100),
						Summary:   fmt.Sprintf("状态：%s（类型：%s）", status, kind),
						Timestamp: ts.Format(time.RFC3339),
						Meta: map[string]any{
							"status": status,
							"kind":   kind,
						},
					}
					if addItem(b, item) {
						b.group.TaskCount++
						perProjectTasks[pKey]++
					}
				}
			}
		}

		// 3. Checkpoints (code diff stats) in [fromTime, toTime], scoped to in-range sessions
		if incCheckpoints {
			rows, err := h.store.QueryRows(ctx, `
				SELECT id, agent_id, session_id, label, workspace, repo_root, branch,
				       files_changed, insertions, deletions, created_at
				FROM session_checkpoints
				ORDER BY created_at DESC
				LIMIT 600`)
			if err == nil {
				type cpAgg struct {
					count   int
					files   int
					ins     int
					del     int
					lastTS  time.Time
					labels  []string
					branch  string
				}
				byProject := make(map[string]*cpAgg)
				for _, r := range rows {
					ts, ok := parseFlexTimeAny(r["created_at"])
					if !ok || ts.Before(fromTime) || ts.After(toTime) {
						continue
					}
					agentID := stringField(r["agent_id"])
					sessID := stringField(r["session_id"])
					meta, found := sessionsByKey[agentID+"\x00"+sessID]
					if !found {
						meta, found = sessionsByID[sessID]
					}
					if found && !meta.inTimeRange {
						continue
					}
					ws := stringField(r["repo_root"])
					if ws == "" {
						ws = stringField(r["workspace"])
					}
					if ws == "" {
						ws = meta.workspace
					}
					pKey, pName := normalizeProjectKey(ws, meta.projectName, "")
					if !projectAllowed(pKey) {
						continue
					}
					ensureBucket(pKey, pName, ws, meta.connectionID != "" && meta.connectionID != "local")

					agg := byProject[pKey]
					if agg == nil {
						agg = &cpAgg{}
						byProject[pKey] = agg
					}
					agg.count++
					agg.files += intField(r["files_changed"])
					agg.ins += intField(r["insertions"])
					agg.del += intField(r["deletions"])
					if ts.After(agg.lastTS) {
						agg.lastTS = ts
					}
					if agg.branch == "" {
						agg.branch = stringField(r["branch"])
					}
					if lbl := strings.TrimSpace(stringField(r["label"])); lbl != "" && len(agg.labels) < 4 {
						agg.labels = append(agg.labels, lbl)
					}
				}
				for pKey, agg := range byProject {
					if agg.count == 0 {
						continue
					}
					b := buckets[pKey]
					if b == nil {
						continue
					}
					b.group.FilesChanged += agg.files
					b.group.Insertions += agg.ins
					b.group.Deletions += agg.del
					summary := fmt.Sprintf("共记录 %d 个工作区检查点，累计涉及 %d 个文件改动（+%d / -%d 行）", agg.count, agg.files, agg.ins, agg.del)
					if agg.branch != "" {
						summary += "，分支：" + agg.branch
					}
					if len(agg.labels) > 0 {
						summary += "；关键节点：" + strings.Join(agg.labels, "、")
					}
					addItem(b, DigestItem{
						ItemID:    stableItemID("checkpoint", pKey, fromTime.Format(time.RFC3339), toTime.Format(time.RFC3339)),
						Kind:      "checkpoint",
						Title:     fmt.Sprintf("工作区代码变更统计（+%d / -%d）", agg.ins, agg.del),
						Summary:   summary,
						Timestamp: agg.lastTS.Format(time.RFC3339),
						Meta: map[string]any{
							"checkpoint_count": agg.count,
							"files_changed":    agg.files,
							"insertions":       agg.ins,
							"deletions":        agg.del,
							"branch":           agg.branch,
						},
					})
				}
			}
		}
	}

	// 4. Local read-only Git log commits for discovered project workspaces
	if incGit {
		seenWorkspaces := make(map[string]bool)
		for _, b := range buckets {
			if b.remote || strings.TrimSpace(b.workspace) == "" {
				continue
			}
			ws := strings.TrimSpace(b.workspace)
			wsNorm := strings.ToLower(ws)
			if seenWorkspaces[wsNorm] {
				continue
			}
			seenWorkspaces[wsNorm] = true
			commits := collectLocalGitCommits(ctx, ws, fromTime, toTime, 50)
			for _, c := range commits {
				summary := fmt.Sprintf("提交 %s（作者：%s）", c.hash, c.author)
				if c.filesChanged > 0 || c.insertions > 0 || c.deletions > 0 {
					summary += fmt.Sprintf(" · %d 个文件 (+%d / -%d)", c.filesChanged, c.insertions, c.deletions)
				}
				item := DigestItem{
					ItemID:    stableItemID("git", b.key, c.hash),
					Kind:      "git_commit",
					Title:     c.subject,
					Summary:   summary,
					Timestamp: c.dateISO,
					Meta: map[string]any{
						"commit_hash":   c.hash,
						"author":        c.author,
						"files_changed": c.filesChanged,
						"insertions":    c.insertions,
						"deletions":     c.deletions,
					},
				}
				if addItem(b, item) {
					b.group.CommitCount++
					// Add git diff stats when checkpoints weren't recorded for this project
					if b.group.FilesChanged == 0 && b.group.Insertions == 0 && b.group.Deletions == 0 {
						b.group.FilesChanged += c.filesChanged
						b.group.Insertions += c.insertions
						b.group.Deletions += c.deletions
					}
				}
			}
		}
	}

	// 4.5 Local office documents updated in [fromTime, toTime]
	if incOfficeDocs {
		seenDocWS := make(map[string]bool)
		for _, b := range buckets {
			if b.remote || strings.TrimSpace(b.workspace) == "" {
				continue
			}
			ws := strings.TrimSpace(b.workspace)
			wsNorm := strings.ToLower(ws)
			if seenDocWS[wsNorm] {
				continue
			}
			seenDocWS[wsNorm] = true
			docs := collectLocalOfficeDocs(ws, fromTime, toTime, 30)
			for _, doc := range docs {
				summary := fmt.Sprintf("本地文档产出（%s · %s）", officeDocTypeLabel(doc.ext), formatHumanBytes(doc.size))
				item := DigestItem{
					ItemID:    stableItemID("office_doc", b.key, doc.relPath),
					Kind:      "office_doc",
					Title:     doc.relPath,
					Summary:   summary,
					Timestamp: doc.modTime.UTC().Format(time.RFC3339),
					Meta: map[string]any{
						"path":     doc.fullPath,
						"rel_path": doc.relPath,
						"ext":      doc.ext,
						"size":     doc.size,
					},
				}
				if addItem(b, item) {
					b.group.OfficeDocCount++
				}
			}
		}
	}

	// 5. DBX SQL execution audit log in [fromTime, toTime]
	if incSQLAudit && strings.TrimSpace(h.auditDir) != "" {
		entries := collectSQLAuditEntries(filepath.Join(h.auditDir, "dbx-sql-audit.jsonl"), fromTime, toTime)
		for _, entry := range entries {
			// Match to known project by session or project_key, else place in DB audit bucket
			var targetBucket *projectBucket
			if entry.SessionID != "" {
				if meta, ok := sessionsByID[entry.SessionID]; ok && projectAllowed(meta.projectKey) {
					targetBucket = ensureBucket(meta.projectKey, meta.projectName, meta.workspace, false)
				}
			}
			if targetBucket == nil && entry.ProjectKey != "" {
				normPK := strings.ToLower(strings.TrimSpace(entry.ProjectKey))
				for k, b := range buckets {
					if k == normPK || strings.Contains(k, normPK) || strings.EqualFold(b.name, entry.ProjectKey) {
						targetBucket = b
						break
					}
				}
			}
			if targetBucket == nil {
				if !projectAllowed("db-operations") {
					continue
				}
				targetBucket = ensureBucket("db-operations", "数据库变更与查询", "", false)
			}

			envTag := "非生产"
			if entry.IsProd {
				envTag = "PROD"
			}
			sourceLabel := strings.TrimSpace(entry.SourceName)
			if sourceLabel == "" {
				sourceLabel = "SQL 代码块"
			}
			statusLabel := "成功"
			if !entry.OK {
				statusLabel = "失败"
			}
			title := fmt.Sprintf("SQL %s · %s (%s)", strings.ToUpper(entry.RiskLevel), sourceLabel, entry.Database)
			summary := fmt.Sprintf("目标：%s / %s [%s] · %d 条语句 · %s · 耗时 %d ms",
				entry.ConnectionName, entry.Database, envTag, max(1, entry.StatementCount), statusLabel, entry.ElapsedMs)
			item := DigestItem{
				ItemID:    stableItemID("sql", entry.Timestamp, entry.SQLHash, entry.ConnectionID, entry.Database),
				Kind:      "sql_audit",
				Title:     title,
				Summary:   summary,
				Timestamp: entry.Timestamp,
				Meta: map[string]any{
					"connection_name": entry.ConnectionName,
					"database":        entry.Database,
					"is_prod":         entry.IsProd,
					"risk_level":      entry.RiskLevel,
					"ok":              entry.OK,
					"elapsed_ms":      entry.ElapsedMs,
				},
			}
			if addItem(targetBucket, item) {
				targetBucket.group.SQLAuditCount++
			}
		}
	}

	// 6. Persisted meeting minutes & decisions in [fromTime, toTime]
	if incMeetings && h.store != nil {
		if pref, err := h.store.GetPreference(ctx, "meetings:records"); err == nil && pref.Exists && pref.Value != nil {
			if raw, err := json.Marshal(pref.Value); err == nil {
				var recs []struct {
					ID          string   `json:"id"`
					Title       string   `json:"title"`
					MeetingDate string   `json:"meeting_date"`
					ProjectKey  string   `json:"project_key"`
					Workspace   string   `json:"workspace"`
					Summary     string   `json:"summary"`
					Decisions   []string `json:"decisions"`
					UpdatedAt   string   `json:"updated_at"`
					Topics      []struct {
						Title  string `json:"title"`
						Detail string `json:"detail"`
					} `json:"topics"`
					ActionItems []struct {
						Text    string `json:"text"`
						Owner   string `json:"owner"`
						Status  string `json:"status"`
						DueDate string `json:"due_date"`
					} `json:"action_items"`
				}
				if json.Unmarshal(raw, &recs) == nil {
					for _, m := range recs {
						ts, ok := parseFlexTimeString(m.MeetingDate, false)
						if !ok {
							ts, ok = parseFlexTimeString(m.UpdatedAt, false)
						}
						if !ok {
							continue
						}
						tsUTC := ts.UTC()
						if tsUTC.Before(fromTime) || tsUTC.After(toTime) {
							continue
						}
						var targetBucket *projectBucket
						if ws := strings.TrimSpace(m.Workspace); ws != "" {
							pKey, pName := normalizeProjectKey(ws, "", "")
							registerAvailable(pKey, pName, ws)
							if !projectAllowed(pKey) {
								continue
							}
							targetBucket = ensureBucket(pKey, pName, ws, false)
						} else if pk := strings.ToLower(strings.TrimSpace(m.ProjectKey)); pk != "" {
							if existing, ok := buckets[pk]; ok {
								if !projectAllowed(pk) {
									continue
								}
								targetBucket = existing
							}
						}
						if targetBucket == nil {
							registerAvailable("meetings", "会议纪要", "")
							if !projectAllowed("meetings") {
								continue
							}
							targetBucket = ensureBucket("meetings", "会议纪要", "", false)
						}
						var parts []string
						if s := strings.TrimSpace(m.Summary); s != "" {
							parts = append(parts, s)
						}
						if len(m.Topics) > 0 {
							topicNames := make([]string, 0, min(len(m.Topics), 4))
							for _, tp := range m.Topics {
								if t := strings.TrimSpace(tp.Title); t != "" {
									topicNames = append(topicNames, t)
									if len(topicNames) >= 4 {
										break
									}
								}
							}
							if len(topicNames) > 0 {
								parts = append(parts, "议题："+strings.Join(topicNames, "、"))
							}
						}
						if len(m.Decisions) > 0 {
							parts = append(parts, "决议："+strings.Join(m.Decisions, "；"))
						}
						if len(m.ActionItems) > 0 {
							openCnt := 0
							actionTexts := make([]string, 0, min(len(m.ActionItems), 4))
							for _, a := range m.ActionItems {
								if a.Status != "done" {
									openCnt++
								}
								if len(actionTexts) < 4 && strings.TrimSpace(a.Text) != "" {
									line := strings.TrimSpace(a.Text)
									if owner := strings.TrimSpace(a.Owner); owner != "" {
										line += "（" + owner + "）"
									}
									actionTexts = append(actionTexts, line)
								}
							}
							line := fmt.Sprintf("行动项：%d 项（待办 %d）", len(m.ActionItems), openCnt)
							if len(actionTexts) > 0 {
								line += "：" + strings.Join(actionTexts, "；")
							}
							parts = append(parts, line)
						}
						if addItem(targetBucket, DigestItem{
							ItemID:    stableItemID("meeting", m.ID),
							Kind:      "meeting",
							Title:     strings.TrimSpace(m.Title),
							Summary:   strings.Join(parts, "\n"),
							Timestamp: tsUTC.Format(time.RFC3339),
							Meta: map[string]any{
								"meeting_id":   m.ID,
								"meeting_date": m.MeetingDate,
							},
						}) {
							targetBucket.group.MeetingCount++
						}
					}
				}
			}
		}
	}

	// Build sorted final groups (exclude empty buckets) & aggregate KPI stats
	groups := make([]DigestProjectGroup, 0, len(buckets))
	var stats DigestSummaryStats
	for _, b := range buckets {
		if len(b.group.Items) == 0 {
			continue
		}
		sort.Slice(b.group.Items, func(i, j int) bool {
			return b.group.Items[i].Timestamp > b.group.Items[j].Timestamp
		})
		groups = append(groups, *b.group)
		stats.ProjectCount++
		stats.SessionCount += b.group.SessionCount
		stats.CommitCount += b.group.CommitCount
		stats.TaskCount += b.group.TaskCount
		stats.SQLAuditCount += b.group.SQLAuditCount
		stats.MeetingCount += b.group.MeetingCount
		stats.OfficeDocCount += b.group.OfficeDocCount
		stats.FilesChanged += b.group.FilesChanged
		stats.Insertions += b.group.Insertions
		stats.Deletions += b.group.Deletions
		for _, it := range b.group.Items {
			if it.Kind == "checkpoint" {
				if cnt, ok := it.Meta["checkpoint_count"].(int); ok {
					stats.CheckpointCount += cnt
				} else {
					stats.CheckpointCount++
				}
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Items) != len(groups[j].Items) {
			return len(groups[i].Items) > len(groups[j].Items)
		}
		return groups[i].ProjectName < groups[j].ProjectName
	})

	available := make([]ProjectOption, 0, len(availableMap))
	for _, opt := range availableMap {
		available = append(available, opt)
	}
	sort.Slice(available, func(i, j int) bool {
		return strings.ToLower(available[i].ProjectName) < strings.ToLower(available[j].ProjectName)
	})

	llmCfg := loadLLMConfig(ctx, h.store)
	return &DigestBundle{
		From:              fromTime.Format(time.RFC3339),
		To:                toTime.Format(time.RFC3339),
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		LLMConfigured:     strings.TrimSpace(llmCfg["api_key"]) != "",
		LLMModel:          llmCfg["model"],
		Projects:          groups,
		AvailableProjects: available,
		Stats:             stats,
	}, nil
}

type gitCommitRecord struct {
	hash         string
	dateISO      string
	author       string
	subject      string
	filesChanged int
	insertions   int
	deletions    int
}

var (
	gitFilesRe = regexp.MustCompile(`(\d+)\s+files?\s+changed`)
	gitInsRe   = regexp.MustCompile(`(\d+)\s+insertions?\(\+\)`)
	gitDelRe   = regexp.MustCompile(`(\d+)\s+deletions?\(-\)`)
)

func collectLocalGitCommits(ctx context.Context, workspace string, from, to time.Time, limit int) []gitCommitRecord {
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return nil
	}
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		gitCtx,
		"git", "-C", workspace, "log",
		"--since="+from.Format(time.RFC3339),
		"--until="+to.Format(time.RFC3339),
		"--no-merges",
		"-n", strconv.Itoa(limit),
		"--date=iso-strict",
		"--pretty=format:@@@%h\x1f%aI\x1f%an\x1f%s",
		"--shortstat",
	)
	configureSubprocess(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}

	var records []gitCommitRecord
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "@@@") {
			parts := strings.Split(strings.TrimPrefix(trimmed, "@@@"), "\x1f")
			if len(parts) < 4 {
				continue
			}
			records = append(records, gitCommitRecord{
				hash:    strings.TrimSpace(parts[0]),
				dateISO: strings.TrimSpace(parts[1]),
				author:  strings.TrimSpace(parts[2]),
				subject: strings.TrimSpace(parts[3]),
			})
		} else if len(records) > 0 && strings.Contains(trimmed, "changed") {
			cur := &records[len(records)-1]
			if m := gitFilesRe.FindStringSubmatch(trimmed); m != nil {
				cur.filesChanged, _ = strconv.Atoi(m[1])
			}
			if m := gitInsRe.FindStringSubmatch(trimmed); m != nil {
				cur.insertions, _ = strconv.Atoi(m[1])
			}
			if m := gitDelRe.FindStringSubmatch(trimmed); m != nil {
				cur.deletions, _ = strconv.Atoi(m[1])
			}
		}
	}
	return records
}

type sqlAuditLine struct {
	Timestamp      string `json:"timestamp"`
	ProjectKey     string `json:"project_key"`
	SessionID      string `json:"session_id"`
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	Database       string `json:"database"`
	IsProd         bool   `json:"is_prod"`
	SourceName     string `json:"source_name"`
	RiskLevel      string `json:"risk_level"`
	StatementCount int    `json:"statement_count"`
	OK             bool   `json:"ok"`
	ElapsedMs      int64  `json:"elapsed_ms"`
	SQLHash        string `json:"sql_hash"`
}

func collectSQLAuditEntries(auditPath string, from, to time.Time) []sqlAuditLine {
	f, err := os.Open(auditPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var result []sqlAuditLine
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec sqlAuditLine
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		ts, ok := parseFlexTimeString(rec.Timestamp, false)
		if !ok {
			continue
		}
		tsUTC := ts.UTC()
		if tsUTC.Before(from) || tsUTC.After(to) {
			continue
		}
		result = append(result, rec)
		if len(result) >= 100 {
			break
		}
	}
	return result
}

var fencedBlockRe = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\\n.*?```")

func stripLargeCodeFences(s string) string {
	return fencedBlockRe.ReplaceAllString(s, "[代码块已省略]")
}

func compactSnippet(s string, maxRunes int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	return truncateRunesClean(cleaned, maxRunes)
}

func truncateRunesClean(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func stringField(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func intField(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

type localOfficeDoc struct {
	fullPath string
	relPath  string
	ext      string
	size     int64
	modTime  time.Time
}

var officeDocExts = map[string]bool{
	".docx": true,
	".xlsx": true,
	".pptx": true,
	".pdf":  true,
	".md":   true,
	".csv":  true,
}

var skipScanDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	"target":       true,
	".idea":        true,
	".vscode":      true,
	".cache":       true,
	"out":          true,
}

func collectLocalOfficeDocs(workspace string, from, to time.Time, limit int) []localOfficeDoc {
	root := filepath.Clean(strings.TrimSpace(workspace))
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	if limit <= 0 {
		limit = 30
	}
	docs := make([]localOfficeDoc, 0, limit)
	visited := 0
	const maxEntries = 2500

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		visited++
		if visited > maxEntries {
			return fs.SkipAll
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		depth := strings.Count(relSlash, "/")
		name := d.Name()
		if d.IsDir() {
			if depth >= 4 || strings.HasPrefix(name, ".") || skipScanDirs[strings.ToLower(name)] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !officeDocExts[ext] {
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		mt := fi.ModTime().UTC()
		if mt.Before(from) || mt.After(to) {
			return nil
		}
		docs = append(docs, localOfficeDoc{
			fullPath: p,
			relPath:  relSlash,
			ext:      ext,
			size:     fi.Size(),
			modTime:  mt,
		})
		return nil
	})

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].modTime.After(docs[j].modTime)
	})
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs
}

func officeDocTypeLabel(ext string) string {
	switch ext {
	case ".docx":
		return "Word 文档"
	case ".xlsx":
		return "Excel 表格"
	case ".pptx":
		return "PPT 演示"
	case ".pdf":
		return "PDF 文档"
	case ".md":
		return "Markdown 文档"
	case ".csv":
		return "CSV 数据表"
	default:
		return "办公文档"
	}
}

func formatHumanBytes(size int64) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
