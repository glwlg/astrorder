package antigravity

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var swarmUUIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

type swarmSummaryRow struct {
	id           string
	title        string
	parentID     string
	nestingDepth int
	agentName    string
	groupID      string
	status       string
	active       bool
	killed       bool
	updatedAt    string
}

type swarmMemberState struct {
	Name                 string
	Role                 string
	IsCoordinator        bool
	ConversationID       string
	ParentConversationID string
	Model                string
	WorkspaceMode        string
	State                string
	Brief                string
	Title                string
	LastMessage          string
	UpdatedAt            string
	SpawnedBy            string
	Killed               bool
	Active               bool
	NestingDepth         int
	Order                int
}

type swarmBoardEntry struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Author               string   `json:"author"`
	AuthorConversationID string   `json:"author_conversation_id,omitempty"`
	Targets              []string `json:"targets,omitempty"`
	Text                 string   `json:"text"`
	CreatedAt            string   `json:"created_at,omitempty"`
	StepIndex            int      `json:"-"`
}

func resolveSwarmRootDir(home, sessionID string, readonly bool) string {
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	deskDir := filepath.Join(home, ".gemini", "antigravity")
	order := []string{cliDir, deskDir}
	if readonly {
		order = []string{deskDir, cliDir}
	}
	for _, dir := range order {
		if _, err := os.Stat(filepath.Join(dir, "conversation_summaries.db")); err == nil {
			if hasSummaryRow(dir, sessionID) {
				return dir
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "brain", sessionID, ".system_generated", "logs", "transcript.jsonl")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "conversations", sessionID+".db")); err == nil {
			return dir
		}
	}
	for _, dir := range order {
		if _, err := os.Stat(filepath.Join(dir, "conversation_summaries.db")); err == nil {
			return dir
		}
	}
	return order[0]
}

func hasSummaryRow(rootDir, sessionID string) bool {
	db, err := openNative(filepath.Join(rootDir, "conversation_summaries.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	var one int
	return db.QueryRow(`SELECT 1 FROM conversation_summaries WHERE conversation_id=?`, sessionID).Scan(&one) == nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) map[string]bool {
	cols := map[string]bool{}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt any
		var pk int
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name != "" {
			cols[strings.ToLower(name)] = true
		}
	}
	return cols
}

func loadSwarmSummaryGraph(ctx context.Context, rootDir string) (map[string]swarmSummaryRow, map[string]string, map[string][]string) {
	byID := map[string]swarmSummaryRow{}
	parentOf := map[string]string{}
	childrenOf := map[string][]string{}

	db, err := openNative(filepath.Join(rootDir, "conversation_summaries.db"))
	if err != nil {
		return byID, parentOf, childrenOf
	}
	defer db.Close()

	cols := tableColumns(ctx, db, "conversation_summaries")
	if !cols["conversation_id"] {
		return byID, parentOf, childrenOf
	}

	colExpr := func(name, fallback string) string {
		if cols[name] {
			return name
		}
		return fallback
	}

	query := fmt.Sprintf(
		`SELECT conversation_id, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM conversation_summaries`,
		colExpr("title", `""`),
		colExpr("parent_conversation_id", `""`),
		colExpr("nesting_depth", `0`),
		colExpr("agent_name", `""`),
		colExpr("group_id", `""`),
		colExpr("status", `""`),
		colExpr("not_fully_idle", `0`),
		colExpr("killed", `0`),
		colExpr("last_modified_time", `""`),
	)

	rows, err := db.QueryContext(ctx, query)
	if err == nil {
		for rows.Next() {
			var r swarmSummaryRow
			if scanErr := rows.Scan(
				&r.id, &r.title, &r.parentID, &r.nestingDepth, &r.agentName,
				&r.groupID, &r.status, &r.active, &r.killed, &r.updatedAt,
			); scanErr != nil {
				continue
			}
			if !validID(r.id) {
				continue
			}
			r.title = strings.TrimSpace(r.title)
			r.parentID = strings.TrimSpace(r.parentID)
			r.agentName = strings.TrimSpace(r.agentName)
			r.groupID = strings.TrimSpace(r.groupID)
			r.status = strings.TrimSpace(r.status)
			if parsed, pErr := time.Parse("2006-01-02 15:04:05.999999999Z07:00", r.updatedAt); pErr == nil {
				r.updatedAt = parsed.UTC().Format(time.RFC3339Nano)
			}
			byID[r.id] = r
			if validID(r.parentID) && r.parentID != r.id {
				parentOf[r.id] = r.parentID
			}
		}
		rows.Close()
	}

	// Merge conversation_parents table if present (captures parent links even if column is empty).
	pRows, pErr := db.QueryContext(ctx, `SELECT conversation_id, parent_id FROM conversation_parents`)
	if pErr == nil {
		for pRows.Next() {
			var cid, pid string
			if pRows.Scan(&cid, &pid) == nil && validID(cid) && validID(pid) && cid != pid {
				if _, exists := parentOf[cid]; !exists {
					parentOf[cid] = pid
				}
				if row, exists := byID[cid]; exists && row.parentID == "" {
					row.parentID = pid
					byID[cid] = row
				}
			}
		}
		pRows.Close()
	}

	for child, parent := range parentOf {
		childrenOf[parent] = append(childrenOf[parent], child)
	}
	for parent := range childrenOf {
		sort.Slice(childrenOf[parent], func(i, j int) bool {
			a, b := byID[childrenOf[parent][i]], byID[childrenOf[parent][j]]
			if a.updatedAt != b.updatedAt {
				return a.updatedAt < b.updatedAt
			}
			return a.id < b.id
		})
	}
	return byID, parentOf, childrenOf
}

func decodeTranscriptStringArg(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		trimmed := strings.TrimSpace(v)
		if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
			var unquoted string
			if json.Unmarshal([]byte(trimmed), &unquoted) == nil {
				return strings.TrimSpace(unquoted)
			}
		}
		return trimmed
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func decodeTranscriptIntArg(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		s := decodeTranscriptStringArg(v)
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

func decodeTranscriptStringSliceArg(raw any) []string {
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := decodeTranscriptStringArg(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "[") {
			var parsed []any
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				return decodeTranscriptStringSliceArg(parsed)
			}
		}
	}
	return nil
}

type transcriptToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type transcriptLine struct {
	StepIndex int                  `json:"step_index"`
	Source    string               `json:"source"`
	Type      string               `json:"type"`
	Status    string               `json:"status"`
	CreatedAt string               `json:"created_at"`
	Model     string               `json:"model"`
	Content   string               `json:"content"`
	ToolCalls []transcriptToolCall `json:"tool_calls"`
}

type parsedSwarmTranscript struct {
	swarmTitle      string
	hasSwarmTool    bool
	hasSubagentTool bool
	lastModel       string
	lastMessage     string
	lastTimestamp   string
	addedMembers    []*swarmMemberState
	modelChanges    map[string]string
	killedNames     map[string]bool
	board           []swarmBoardEntry
}

func parseSwarmTranscript(rootDir, convID, authorName string) parsedSwarmTranscript {
	out := parsedSwarmTranscript{
		modelChanges: map[string]string{},
		killedNames:  map[string]bool{},
	}
	tPath := filepath.Join(rootDir, "brain", convID, ".system_generated", "logs", "transcript.jsonl")
	st, err := os.Stat(tPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 24*1024*1024 {
		return out
	}
	f, err := os.Open(tPath)
	if err != nil {
		return out
	}
	defer f.Close()

	// Track pending invoke_subagent / swarm add calls so we can match conversationId from the next GENERIC step.
	var pendingSubagents []*swarmMemberState
	var pendingSwarmAdds []*swarmMemberState
	order := 0

	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var entry transcriptLine
			if json.Unmarshal(line, &entry) == nil {
				if entry.CreatedAt != "" {
					out.lastTimestamp = entry.CreatedAt
				}
				if entry.Type == "PLANNER_RESPONSE" {
					if m := strings.TrimSpace(entry.Model); m != "" {
						out.lastModel = m
					}
					if txt := strings.TrimSpace(entry.Content); txt != "" {
						out.lastMessage = truncateSwarmSnippet(txt, 220)
					}
					for tcIdx, tc := range entry.ToolCalls {
						switch tc.Name {
						case "swarm":
							out.hasSwarmTool = true
							action := strings.ToLower(decodeTranscriptStringArg(tc.Args["action"]))
							switch action {
							case "title":
								if t := decodeTranscriptStringArg(tc.Args["title"]); t != "" {
									out.swarmTitle = t
								}
							case "add":
								baseName := decodeTranscriptStringArg(tc.Args["name"])
								brief := decodeTranscriptStringArg(tc.Args["brief"])
								model := decodeTranscriptStringArg(tc.Args["model"])
								ws := decodeTranscriptStringArg(tc.Args["workspace"])
								if ws == "" {
									ws = "inherit"
								}
								n := decodeTranscriptIntArg(tc.Args["n"])
								tasks := decodeTranscriptStringSliceArg(tc.Args["tasks"])

								var targets []string
								switch {
								case len(tasks) > 0 && baseName != "":
									for i, task := range tasks {
										memberName := fmt.Sprintf("%s-%d", baseName, i+1)
										targets = append(targets, memberName)
										memberBrief := brief
										if task != "" {
											if memberBrief != "" {
												memberBrief = task + "\n\n" + memberBrief
											} else {
												memberBrief = task
											}
										}
										order++
										m := &swarmMemberState{
											Name:          memberName,
											Role:          "worker",
											Model:         model,
											WorkspaceMode: ws,
											Brief:         memberBrief,
											SpawnedBy:     authorName,
											UpdatedAt:     entry.CreatedAt,
											Order:         order,
										}
										out.addedMembers = append(out.addedMembers, m)
										pendingSwarmAdds = append(pendingSwarmAdds, m)
									}
								case n > 1 && baseName != "":
									for i := 1; i <= n; i++ {
										memberName := fmt.Sprintf("%s-%d", baseName, i)
										targets = append(targets, memberName)
										order++
										m := &swarmMemberState{
											Name:          memberName,
											Role:          "worker",
											Model:         model,
											WorkspaceMode: ws,
											Brief:         brief,
											SpawnedBy:     authorName,
											UpdatedAt:     entry.CreatedAt,
											Order:         order,
										}
										out.addedMembers = append(out.addedMembers, m)
										pendingSwarmAdds = append(pendingSwarmAdds, m)
									}
								case baseName != "":
									targets = []string{baseName}
									order++
									m := &swarmMemberState{
										Name:          baseName,
										Role:          "worker",
										Model:         model,
										WorkspaceMode: ws,
										Brief:         brief,
										SpawnedBy:     authorName,
										UpdatedAt:     entry.CreatedAt,
										Order:         order,
									}
									out.addedMembers = append(out.addedMembers, m)
									pendingSwarmAdds = append(pendingSwarmAdds, m)
								}
								if brief != "" || len(targets) > 0 {
									text := brief
									if text == "" {
										text = "已加入团队并等待分工"
									}
									out.board = append(out.board, swarmBoardEntry{
										ID:                   fmt.Sprintf("%s:%d:%d:add", convID, entry.StepIndex, tcIdx),
										Kind:                 "brief",
										Author:               authorName,
										AuthorConversationID: convID,
										Targets:              targets,
										Text:                 truncateSwarmSnippet(text, 600),
										CreatedAt:            entry.CreatedAt,
										StepIndex:            entry.StepIndex,
									})
								}
							case "model":
								target := decodeTranscriptStringArg(tc.Args["name"])
								if target == "" {
									target = authorName
								}
								model := decodeTranscriptStringArg(tc.Args["model"])
								if target != "" && model != "" {
									out.modelChanges[target] = model
									out.board = append(out.board, swarmBoardEntry{
										ID:                   fmt.Sprintf("%s:%d:%d:model", convID, entry.StepIndex, tcIdx),
										Kind:                 "model_change",
										Author:               authorName,
										AuthorConversationID: convID,
										Targets:              []string{target},
										Text:                 fmt.Sprintf("切换模型为 %s", model),
										CreatedAt:            entry.CreatedAt,
										StepIndex:            entry.StepIndex,
									})
								}
							case "kill":
								target := decodeTranscriptStringArg(tc.Args["name"])
								if target != "" {
									out.killedNames[target] = true
									out.board = append(out.board, swarmBoardEntry{
										ID:                   fmt.Sprintf("%s:%d:%d:kill", convID, entry.StepIndex, tcIdx),
										Kind:                 "kill",
										Author:               authorName,
										AuthorConversationID: convID,
										Targets:              []string{target},
										Text:                 fmt.Sprintf("已结束并回收成员 @%s", target),
										CreatedAt:            entry.CreatedAt,
										StepIndex:            entry.StepIndex,
									})
								}
							case "post", "message":
								msg := firstNonEmptyString(
									decodeTranscriptStringArg(tc.Args["message"]),
									decodeTranscriptStringArg(tc.Args["text"]),
									decodeTranscriptStringArg(tc.Args["body"]),
								)
								if msg != "" {
									targets := decodeTranscriptStringSliceArg(tc.Args["targets"])
									if to := firstNonEmptyString(decodeTranscriptStringArg(tc.Args["to"]), decodeTranscriptStringArg(tc.Args["name"])); to != "" {
										targets = append(targets, to)
									}
									out.board = append(out.board, swarmBoardEntry{
										ID:                   fmt.Sprintf("%s:%d:%d:post", convID, entry.StepIndex, tcIdx),
										Kind:                 "message",
										Author:               authorName,
										AuthorConversationID: convID,
										Targets:              targets,
										Text:                 truncateSwarmSnippet(msg, 600),
										CreatedAt:            entry.CreatedAt,
										StepIndex:            entry.StepIndex,
									})
								}
							}
						case "send_message", "post", "swarm_post", "board_post":
							recipient := firstNonEmptyString(
								decodeTranscriptStringArg(tc.Args["Recipient"]),
								decodeTranscriptStringArg(tc.Args["recipient"]),
								decodeTranscriptStringArg(tc.Args["to"]),
							)
							msg := firstNonEmptyString(
								decodeTranscriptStringArg(tc.Args["Message"]),
								decodeTranscriptStringArg(tc.Args["message"]),
								decodeTranscriptStringArg(tc.Args["text"]),
								decodeTranscriptStringArg(tc.Args["body"]),
							)
							if msg != "" {
								var targets []string
								if recipient != "" {
									targets = []string{recipient}
								}
								out.board = append(out.board, swarmBoardEntry{
									ID:                   fmt.Sprintf("%s:%d:%d:msg", convID, entry.StepIndex, tcIdx),
									Kind:                 "message",
									Author:               authorName,
									AuthorConversationID: convID,
									Targets:              targets,
									Text:                 truncateSwarmSnippet(msg, 600),
									CreatedAt:            entry.CreatedAt,
									StepIndex:            entry.StepIndex,
								})
							}
						case "invoke_subagent":
							out.hasSubagentTool = true
							role := decodeTranscriptStringArg(tc.Args["Role"])
							typeName := decodeTranscriptStringArg(tc.Args["TypeName"])
							prompt := decodeTranscriptStringArg(tc.Args["Prompt"])
							model := decodeTranscriptStringArg(tc.Args["Model"])
							ws := decodeTranscriptStringArg(tc.Args["Workspace"])
							if ws == "" {
								ws = "inherit"
							}
							name := role
							if name == "" {
								name = typeName
							}
							if name == "" {
								name = fmt.Sprintf("subagent-%d", order+1)
							}
							order++
							m := &swarmMemberState{
								Name:          name,
								Role:          typeName,
								Model:         model,
								WorkspaceMode: ws,
								Brief:         prompt,
								SpawnedBy:     authorName,
								UpdatedAt:     entry.CreatedAt,
								Order:         order,
							}
							out.addedMembers = append(out.addedMembers, m)
							pendingSubagents = append(pendingSubagents, m)
							if prompt != "" {
								out.board = append(out.board, swarmBoardEntry{
									ID:                   fmt.Sprintf("%s:%d:%d:subagent", convID, entry.StepIndex, tcIdx),
									Kind:                 "brief",
									Author:               authorName,
									AuthorConversationID: convID,
									Targets:              []string{name},
									Text:                 truncateSwarmSnippet(prompt, 600),
									CreatedAt:            entry.CreatedAt,
									StepIndex:            entry.StepIndex,
								})
							}
						}
					}
				} else if entry.Type == "GENERIC" && entry.Content != "" {
					// Match conversationId returned by invoke_subagent or swarm add tool result.
					if len(pendingSubagents) > 0 {
						if cid := swarmUUIDRe.FindString(entry.Content); validID(cid) {
							pendingSubagents[0].ConversationID = cid
							pendingSubagents = pendingSubagents[1:]
						}
					} else if len(pendingSwarmAdds) > 0 {
						found := swarmUUIDRe.FindAllString(entry.Content, -1)
						for _, cid := range found {
							if len(pendingSwarmAdds) == 0 {
								break
							}
							if validID(cid) {
								pendingSwarmAdds[0].ConversationID = cid
								pendingSwarmAdds = pendingSwarmAdds[1:]
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			break
		}
	}
	return out
}

func truncateSwarmSnippet(raw string, maxRunes int) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if len(runes) <= maxRunes {
		return clean
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

func readTranscriptFallbackMessages(rootDir, convID string, limit int) []map[string]any {
	tPath := filepath.Join(rootDir, "brain", convID, ".system_generated", "logs", "transcript.jsonl")
	st, err := os.Stat(tPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 24*1024*1024 {
		return nil
	}
	f, err := os.Open(tPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var items []map[string]any
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var entry transcriptLine
			if json.Unmarshal(line, &entry) == nil {
				switch entry.Type {
				case "USER_INPUT":
					if txt := strings.TrimSpace(entry.Content); txt != "" {
						items = append(items, map[string]any{
							"id":         fmt.Sprintf("antigravity:%s:%d", convID, entry.StepIndex),
							"role":       "user",
							"kind":       "message",
							"text":       txt,
							"created_at": entry.CreatedAt,
						})
					}
				case "PLANNER_RESPONSE":
					if txt := strings.TrimSpace(entry.Content); txt != "" {
						items = append(items, map[string]any{
							"id":         fmt.Sprintf("antigravity:%s:%d", convID, entry.StepIndex),
							"role":       "assistant",
							"kind":       "message",
							"text":       txt,
							"created_at": entry.CreatedAt,
						})
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items
}

func readNativeSessionSwarm(ctx context.Context, home, sessionID string, readonly bool, memberConversationID string, memberLimit int) (map[string]any, error) {
	if !validID(sessionID) {
		return nil, fmt.Errorf("invalid Antigravity conversation ID")
	}
	rootDir := resolveSwarmRootDir(home, sessionID, readonly)
	byID, parentOf, childrenOf := loadSwarmSummaryGraph(ctx, rootDir)

	// Walk upward to locate the Swarm / team coordinator root.
	rootID := sessionID
	visitedUp := map[string]bool{rootID: true}
	for depth := 0; depth < 16; depth++ {
		pid := parentOf[rootID]
		if !validID(pid) || visitedUp[pid] {
			break
		}
		visitedUp[pid] = true
		rootID = pid
	}

	// Collect all descendants under rootID.
	var descendantIDs []string
	queue := append([]string{}, childrenOf[rootID]...)
	seenDesc := map[string]bool{rootID: true}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if seenDesc[curr] {
			continue
		}
		seenDesc[curr] = true
		descendantIDs = append(descendantIDs, curr)
		queue = append(queue, childrenOf[curr]...)
	}

	// Fallback: if conversations share a non-empty group_id without parent links,
	// gather group peers and promote the nesting_depth=0/coordinator row to rootID.
	if gid := byID[rootID].groupID; gid != "" {
		var peers []swarmSummaryRow
		for _, r := range byID {
			if r.groupID == gid {
				peers = append(peers, r)
			}
		}
		if len(peers) > 1 {
			sort.Slice(peers, func(i, j int) bool {
				if peers[i].nestingDepth != peers[j].nestingDepth {
					return peers[i].nestingDepth < peers[j].nestingDepth
				}
				if peers[i].updatedAt != peers[j].updatedAt {
					return peers[i].updatedAt < peers[j].updatedAt
				}
				return peers[i].id < peers[j].id
			})
			if len(descendantIDs) == 0 && parentOf[rootID] == "" {
				for _, p := range peers {
					if p.nestingDepth == 0 && (p.agentName == "" || strings.EqualFold(p.agentName, "coordinator")) {
						rootID = p.id
						break
					}
				}
			}
			seenDesc = map[string]bool{rootID: true}
			for _, id := range descendantIDs {
				seenDesc[id] = true
			}
			for _, p := range peers {
				if !seenDesc[p.id] {
					seenDesc[p.id] = true
					descendantIDs = append(descendantIDs, p.id)
				}
			}
		}
	}

	rootParsed := parseSwarmTranscript(rootDir, rootID, "coordinator")
	hasSwarmTool := rootParsed.hasSwarmTool
	hasSubagentTool := rootParsed.hasSubagentTool

	// Build coordinator + member map.
	rootSummary := byID[rootID]
	coordModel := rootParsed.lastModel
	if m, ok := rootParsed.modelChanges["coordinator"]; ok && m != "" {
		coordModel = m
	}
	coordinator := &swarmMemberState{
		Name:           "coordinator",
		Role:           "协调者",
		IsCoordinator:  true,
		ConversationID: rootID,
		Model:          coordModel,
		WorkspaceMode:  "inherit",
		Title:          rootSummary.title,
		LastMessage:    rootParsed.lastMessage,
		UpdatedAt:      firstNonEmptyString(rootSummary.updatedAt, rootParsed.lastTimestamp),
		Active:         rootSummary.active,
		Killed:         rootSummary.killed,
		NestingDepth:   0,
		Order:          0,
	}

	membersByName := map[string]*swarmMemberState{}
	membersByConvID := map[string]*swarmMemberState{rootID: coordinator}
	var orderedWorkers []*swarmMemberState
	nextOrder := 1

	upsertWorker := func(candidate *swarmMemberState) *swarmMemberState {
		if candidate.ConversationID != "" {
			if existing := membersByConvID[candidate.ConversationID]; existing != nil {
				mergeSwarmMember(existing, candidate)
				if existing.Name != "" {
					membersByName[existing.Name] = existing
				}
				return existing
			}
		}
		if candidate.Name != "" {
			if existing := membersByName[candidate.Name]; existing != nil {
				mergeSwarmMember(existing, candidate)
				if existing.ConversationID != "" {
					membersByConvID[existing.ConversationID] = existing
				}
				return existing
			}
		}
		if candidate.Order == 0 {
			candidate.Order = nextOrder
			nextOrder++
		} else if candidate.Order >= nextOrder {
			nextOrder = candidate.Order + 1
		}
		orderedWorkers = append(orderedWorkers, candidate)
		if candidate.Name != "" {
			membersByName[candidate.Name] = candidate
		}
		if candidate.ConversationID != "" {
			membersByConvID[candidate.ConversationID] = candidate
		}
		return candidate
	}

	for _, added := range rootParsed.addedMembers {
		cp := *added
		if cp.ParentConversationID == "" {
			cp.ParentConversationID = rootID
		}
		cp.NestingDepth = 1
		upsertWorker(&cp)
	}

	// Merge database descendants and parse their transcripts too (for helper workers & board replies).
	board := append([]swarmBoardEntry{}, rootParsed.board...)
	swarmTitle := rootParsed.swarmTitle
	killedNames := map[string]bool{}
	for k, v := range rootParsed.killedNames {
		killedNames[k] = v
	}
	modelChanges := map[string]string{}
	for k, v := range rootParsed.modelChanges {
		modelChanges[k] = v
	}

	for _, descID := range descendantIDs {
		row := byID[descID]
		name := row.agentName
		if name == "" {
			if existing := membersByConvID[descID]; existing != nil && existing.Name != "" {
				name = existing.Name
			} else if row.title != "" {
				name = row.title
			} else {
				name = descID[:8]
			}
		}
		depth := row.nestingDepth
		if depth <= 0 {
			depth = 1
		}
		workerParsed := parseSwarmTranscript(rootDir, descID, name)
		if workerParsed.hasSwarmTool {
			hasSwarmTool = true
		}
		if workerParsed.hasSubagentTool {
			hasSubagentTool = true
		}
		if swarmTitle == "" && workerParsed.swarmTitle != "" {
			swarmTitle = workerParsed.swarmTitle
		}
		for k, v := range workerParsed.killedNames {
			killedNames[k] = v
		}
		for k, v := range workerParsed.modelChanges {
			modelChanges[k] = v
		}
		board = append(board, workerParsed.board...)

		member := upsertWorker(&swarmMemberState{
			Name:                 name,
			Role:                 "worker",
			ConversationID:       descID,
			ParentConversationID: row.parentID,
			Model:                workerParsed.lastModel,
			Title:                row.title,
			LastMessage:          workerParsed.lastMessage,
			UpdatedAt:            firstNonEmptyString(row.updatedAt, workerParsed.lastTimestamp),
			Active:               row.active,
			Killed:               row.killed,
			NestingDepth:         depth,
		})
		for _, helper := range workerParsed.addedMembers {
			hcp := *helper
			if hcp.ParentConversationID == "" {
				hcp.ParentConversationID = descID
			}
			hcp.NestingDepth = member.NestingDepth + 1
			upsertWorker(&hcp)
		}
	}

	// Apply model changes and kill flags.
	for name, model := range modelChanges {
		if name == "coordinator" {
			coordinator.Model = model
		} else if m := membersByName[name]; m != nil {
			m.Model = model
		}
	}
	for name, killed := range killedNames {
		if killed {
			if m := membersByName[name]; m != nil {
				m.Killed = true
			}
		}
	}

	// Resolve recipient conversation IDs in board entries into readable member names.
	for i := range board {
		for j, target := range board[i].Targets {
			if m := membersByConvID[target]; m != nil && m.Name != "" {
				board[i].Targets[j] = m.Name
			}
		}
	}

	// Determine member states and aggregate summary.
	allMembers := make([]*swarmMemberState, 0, 1+len(orderedWorkers))
	allMembers = append(allMembers, coordinator)
	allMembers = append(allMembers, orderedWorkers...)

	anyWorking := false
	for _, m := range allMembers {
		if m.Active && !m.Killed {
			anyWorking = true
		}
	}

	summary := map[string]int{
		"total":   len(allMembers),
		"working": 0,
		"waiting": 0,
		"stopped": 0,
		"killed":  0,
	}
	latestUpdated := coordinator.UpdatedAt
	for _, m := range allMembers {
		switch {
		case m.Killed:
			m.State = "killed"
			summary["killed"]++
		case m.Active:
			m.State = "working"
			summary["working"]++
		case anyWorking:
			m.State = "waiting"
			summary["waiting"]++
		default:
			if m.ConversationID != "" {
				m.State = "stopped"
				summary["stopped"]++
			} else {
				m.State = "waiting"
				summary["waiting"]++
			}
		}
		if m.UpdatedAt > latestUpdated {
			latestUpdated = m.UpdatedAt
		}
	}

	// Sort board chronologically and cap to latest 100 items.
	sort.SliceStable(board, func(i, j int) bool {
		if board[i].CreatedAt != board[j].CreatedAt {
			return board[i].CreatedAt < board[j].CreatedAt
		}
		return board[i].StepIndex < board[j].StepIndex
	})
	if len(board) > 100 {
		board = board[len(board)-100:]
	}

	available := hasSwarmTool || len(orderedWorkers) > 0 || len(board) > 0
	kind := "swarm"
	if !hasSwarmTool && (hasSubagentTool || len(orderedWorkers) > 0) {
		kind = "subagents"
	}
	if swarmTitle == "" {
		swarmTitle = rootSummary.title
	}
	if swarmTitle == "" && available {
		if kind == "swarm" {
			swarmTitle = "Antigravity Swarm"
		} else {
			swarmTitle = "子代理协作"
		}
	}

	memberPayloads := make([]map[string]any, 0, len(allMembers))
	allowedMemberIDs := map[string]bool{rootID: true}
	for _, m := range allMembers {
		if m.ConversationID != "" {
			allowedMemberIDs[m.ConversationID] = true
		}
		memberPayloads = append(memberPayloads, map[string]any{
			"name":                   m.Name,
			"role":                   m.Role,
			"is_coordinator":         m.IsCoordinator,
			"conversation_id":        m.ConversationID,
			"parent_conversation_id": m.ParentConversationID,
			"model":                  m.Model,
			"workspace_mode":         m.WorkspaceMode,
			"state":                  m.State,
			"brief":                  m.Brief,
			"title":                  m.Title,
			"last_message":           m.LastMessage,
			"updated_at":             m.UpdatedAt,
			"spawned_by":             m.SpawnedBy,
			"nesting_depth":          m.NestingDepth,
		})
	}

	result := map[string]any{
		"supported":              true,
		"available":              available,
		"active":                 available && anyWorking,
		"kind":                   kind,
		"title":                  swarmTitle,
		"coordinator_session_id": rootID,
		"current_session_id":     sessionID,
		"is_coordinator":         sessionID == rootID,
		"summary":                summary,
		"members":                memberPayloads,
		"board":                  board,
		"updated_at":             latestUpdated,
	}

	if memberConversationID != "" {
		if !validID(memberConversationID) || !allowedMemberIDs[memberConversationID] {
			return nil, fmt.Errorf("requested member conversation does not belong to this Swarm")
		}
		limit := memberLimit
		if limit <= 0 {
			limit = 30
		}
		if limit > 60 {
			limit = 60
		}
		convDir := filepath.Join(rootDir, "conversations")
		rawItems, _, err := readHistory(ctx, convDir, memberConversationID, "", limit)
		if err != nil || len(rawItems) == 0 {
			if fallback := readTranscriptFallbackMessages(rootDir, memberConversationID, limit); len(fallback) > 0 {
				rawItems = fallback
			} else if rawItems == nil {
				rawItems = []map[string]any{}
			}
		}
		previewItems := make([]map[string]any, 0, len(rawItems))
		for _, item := range rawItems {
			entry := map[string]any{
				"id":         fmt.Sprint(item["id"]),
				"role":       fmt.Sprint(item["role"]),
				"kind":       fmt.Sprint(item["kind"]),
				"text":       truncateSwarmSnippet(fmt.Sprint(item["text"]), 2000),
				"created_at": fmt.Sprint(item["created_at"]),
			}
			if toolObj, ok := item["tool"].(map[string]any); ok && toolObj != nil {
				if name, ok := toolObj["name"].(string); ok && name != "" {
					entry["tool_name"] = name
				}
				if status, ok := toolObj["status"].(string); ok && status != "" {
					entry["tool_status"] = status
				}
			}
			previewItems = append(previewItems, entry)
		}
		result["member_conversation_id"] = memberConversationID
		result["member_messages"] = previewItems
	}

	return result, nil
}

func mergeSwarmMember(dst, src *swarmMemberState) {
	if dst.Name == "" && src.Name != "" {
		dst.Name = src.Name
	}
	if dst.Role == "" && src.Role != "" {
		dst.Role = src.Role
	}
	if dst.ConversationID == "" && src.ConversationID != "" {
		dst.ConversationID = src.ConversationID
	}
	if dst.ParentConversationID == "" && src.ParentConversationID != "" {
		dst.ParentConversationID = src.ParentConversationID
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if dst.WorkspaceMode == "" && src.WorkspaceMode != "" {
		dst.WorkspaceMode = src.WorkspaceMode
	}
	if dst.Brief == "" && src.Brief != "" {
		dst.Brief = src.Brief
	}
	if dst.Title == "" && src.Title != "" {
		dst.Title = src.Title
	}
	if src.LastMessage != "" {
		dst.LastMessage = src.LastMessage
	}
	if src.UpdatedAt > dst.UpdatedAt {
		dst.UpdatedAt = src.UpdatedAt
	}
	if dst.SpawnedBy == "" && src.SpawnedBy != "" {
		dst.SpawnedBy = src.SpawnedBy
	}
	if src.Active {
		dst.Active = true
	}
	if src.Killed {
		dst.Killed = true
	}
	if src.NestingDepth > dst.NestingDepth {
		dst.NestingDepth = src.NestingDepth
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (a *Adapter) readSessionSwarm(ctx context.Context, sessionID string, readonly bool, memberConversationID string, memberLimit int) (map[string]any, error) {
	if !validID(sessionID) {
		return nil, fmt.Errorf("invalid Antigravity conversation ID")
	}
	if a.remoteHelper != nil {
		req := map[string]any{
			"domain":          "antigravity",
			"action":          "session_swarm",
			"conversation_id": sessionID,
			"readonly":        readonly,
		}
		if memberConversationID != "" {
			req["member_conversation_id"] = memberConversationID
		}
		if memberLimit > 0 {
			req["member_limit"] = memberLimit
		}
		return a.remoteHelper(ctx, req)
	}
	return readNativeSessionSwarm(ctx, a.config.Home, sessionID, readonly, memberConversationID, memberLimit)
}
