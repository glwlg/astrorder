// Package botgroups implements persistent group orchestration using native Agent sessions.
package botgroups

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

type Member struct {
	MachineID        string  `json:"machine_id"`
	AgentID          string  `json:"agent_id"`
	Name             *string `json:"name"`
	Alias            *string `json:"alias"`
	SystemRolePrompt *string `json:"system_role_prompt"`
	ModelProvider    *string `json:"model_provider"`
	ModelName        *string `json:"model_name"`
	ThinkingEffort   *string `json:"thinking_effort"`
	ApprovalPolicy   *string `json:"approval_policy"`
	Workspace        *string `json:"workspace"`
}
type MemberState struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
	LastError string `json:"last_error,omitempty"`
	UpdatedAt string `json:"updated_at"`
}
type Group struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Members        []Member               `json:"members"`
	MaxHops        int                    `json:"max_hops"`
	ActiveHop      int                    `json:"active_hop"`
	ActiveSpeaker  *string                `json:"active_speaker_agent_id"`
	ActiveSpeakers []string               `json:"active_speakers"`
	MemberStates   map[string]MemberState `json:"member_states"`
	CreatedAt      string                 `json:"created_at"`
	UpdatedAt      string                 `json:"updated_at"`
	StoppedAt      string                 `json:"stopped_at,omitempty"`
}
type Message struct {
	ID         string   `json:"id"`
	GroupID    string   `json:"group_id"`
	SenderType string   `json:"sender_type"`
	SenderID   string   `json:"sender_id"`
	SenderName string   `json:"sender_name"`
	Text       string   `json:"text"`
	Mentions   []string `json:"mentions"`
	HopCount   int      `json:"hop_count"`
	CreatedAt  string   `json:"created_at"`
}
type Turn struct {
	GroupID   string
	Member    Member
	Prompt    string
	Hop       int
	CommandID string
	SessionID string
}

// Controller binds one exact session and returns a completed native reply.
// Interrupt is called only by an explicit group stop or deletion.
type Controller interface {
	Run(context.Context, Turn, func(string) error) (string, error)
	Interrupt(context.Context, Turn) error
}
type Publisher interface{ PublishCommitted([]store.Event) }
type Options struct {
	Store      *store.Store
	Controller Controller
	Authorizer *auth.Authenticator
	Publisher  Publisher
}
type activeTurn struct {
	turn         Turn
	cancel       context.CancelFunc
	explicitStop bool
}
type Service struct {
	options      Options
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	submissionMu sync.Mutex
	active       map[string]map[string]*activeTurn
	wg           sync.WaitGroup
	closed       bool
}

var errMissing = errors.New("群聊未找到")

func New(ctx context.Context, options Options) (*Service, error) {
	if options.Store == nil || options.Controller == nil || options.Authorizer == nil || options.Publisher == nil {
		return nil, errors.New("group store, controller, authentication and event publisher are required")
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{options: options, ctx: ctx, cancel: cancel, active: make(map[string]map[string]*activeTurn)}
	groups, err := s.list(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, g := range groups {
		needsRecovery := false
		for _, state := range g.MemberStates {
			if state.Status == "running" {
				needsRecovery = true
			}
		}
		if needsRecovery {
			if _, err = s.mutate(ctx, g.ID, func(group *Group) error {
				for id, state := range group.MemberStates {
					if state.Status == "running" {
						state.Status = "unknown"
						state.LastError = "App 已重新连接，原生执行结果未确认；不会自动重发。"
						state.UpdatedAt = now()
						group.MemberStates[id] = state
					}
				}
				return nil
			}); err != nil {
				cancel()
				return nil, err
			}
		}
	}
	return s, nil
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }
func Matches(method, path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/bot-groups"), "/")
	if path == "/api/v1/bot-groups" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if !strings.HasPrefix(path, "/api/v1/bot-groups/") || len(parts) < 2 || parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		return method == http.MethodGet || method == http.MethodPatch || method == http.MethodDelete
	}
	return len(parts) == 3 && ((parts[2] == "messages" && (method == http.MethodGet || method == http.MethodPost)) || (parts[2] == "stop" && method == http.MethodPost))
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if failure := s.options.Authorizer.AuthorizeBrowser(r); failure != nil {
		writeError(w, failure.Status, failure.Detail)
		return
	}
	if !Matches(r.Method, r.URL.Path) {
		writeError(w, 404, "Not Found")
		return
	}
	var result any
	var err error
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/bot-groups"), "/")
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			var groups []Group
			groups, err = s.list(r.Context())
			result = map[string]any{"items": groups, "count": len(groups)}
		} else {
			var payload map[string]json.RawMessage
			payload, err = readPayload(w, r)
			if err == nil {
				var group Group
				group, err = s.create(r.Context(), payload)
				result = map[string]any{"group": group, "ok": true}
			}
		}
	} else {
		id := parts[1]
		switch {
		case len(parts) == 2 && r.Method == http.MethodGet:
			var group Group
			group, err = s.get(r.Context(), id)
			result = map[string]any{"group": group}
		case len(parts) == 2 && r.Method == http.MethodPatch:
			var payload map[string]json.RawMessage
			payload, err = readPayload(w, r)
			if err == nil {
				var group Group
				group, err = s.update(r.Context(), id, payload)
				result = map[string]any{"group": group, "ok": true}
			}
		case len(parts) == 2 && r.Method == http.MethodDelete:
			err = s.remove(r.Context(), id)
			result = map[string]any{"ok": true, "id": id}
		case parts[2] == "stop":
			var group Group
			group, err = s.stop(r.Context(), id)
			result = map[string]any{"ok": err == nil, "group": group}
		case r.Method == http.MethodGet:
			var messages []Message
			messages, err = s.messages(r.Context(), id)
			result = map[string]any{"items": messages, "count": len(messages)}
		default:
			var payload map[string]json.RawMessage
			payload, err = readPayload(w, r)
			if err == nil {
				result, err = s.send(r.Context(), id, payload)
			}
		}
	}
	if err != nil {
		code := 422
		if errors.Is(err, errMissing) {
			code = 404
		}
		var failure *serviceError
		if errors.As(err, &failure) {
			code = failure.code
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, result)
}

type serviceError struct {
	code int
	err  error
}

func (e *serviceError) Error() string { return e.err.Error() }
func (e *serviceError) Unwrap() error { return e.err }
func now() string                     { return time.Now().UTC().Format(time.RFC3339Nano) }
func randomID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, code int, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": detail})
}
func readPayload(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	var value map[string]json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("Invalid request")
	}
	if value == nil {
		return nil, errors.New("Invalid request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("Invalid request")
	}
	return value, nil
}
func (s *Service) get(ctx context.Context, id string) (Group, error) {
	preference, err := s.options.Store.GetPreference(ctx, "bot_group:"+id)
	if err != nil {
		return Group{}, err
	}
	if !preference.Exists || preference.Value == nil {
		return Group{}, errMissing
	}
	raw, err := json.Marshal(preference.Value)
	if err != nil {
		return Group{}, err
	}
	var group Group
	err = json.Unmarshal(raw, &group)
	return group, err
}
func (s *Service) list(ctx context.Context) ([]Group, error) {
	preferences, err := s.options.Store.ListPreferences(ctx)
	if err != nil {
		return nil, err
	}
	groups := []Group{}
	for key, value := range preferences {
		if strings.HasPrefix(key, "bot_group:") && value != nil {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var group Group
			if err = json.Unmarshal(raw, &group); err != nil {
				return nil, err
			}
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].CreatedAt > groups[j].CreatedAt })
	return groups, nil
}
func (s *Service) messages(ctx context.Context, id string) ([]Message, error) {
	value, err := s.options.Store.GetPreference(ctx, "bot_group_msgs:"+id)
	if err != nil {
		return nil, err
	}
	messages := []Message{}
	if value.Exists && value.Value != nil {
		raw, err := json.Marshal(value.Value)
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(raw, &messages)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}
func readGroupTx(ctx context.Context, tx *sql.Tx, id string) (Group, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, "bot_group:"+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, errMissing
	}
	if err != nil {
		return Group{}, err
	}
	var group Group
	err = json.Unmarshal([]byte(raw), &group)
	return group, err
}
func putTx(ctx context.Context, tx *sql.Tx, key string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences(key,value)VALUES(?,?) ON CONFLICT(key)DO UPDATE SET value=excluded.value`, key, string(body))
	return err
}
func appendEventTx(ctx context.Context, tx *sql.Tx, kind string, value any) (store.Event, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return store.Event{}, err
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err = decoder.Decode(&data); err != nil {
		return store.Event{}, err
	}
	id, err := randomID("group-event-")
	if err != nil {
		return store.Event{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO events(event_id,type,agent_id,session_id,data,created_at)VALUES(?,?,NULL,NULL,?,?)`, id, kind, string(body), now())
	if err != nil {
		return store.Event{}, err
	}
	cursor, err := result.LastInsertId()
	return store.Event{ID: id, Cursor: cursor, Type: kind, Data: data}, err
}
func (s *Service) mutate(ctx context.Context, id string, fn func(*Group) error) (Group, error) {
	var group Group
	var event store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		group, err = readGroupTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = fn(&group); err != nil {
			return err
		}
		group.UpdatedAt = now()
		if err = putTx(ctx, tx, "bot_group:"+id, group); err != nil {
			return err
		}
		event, err = appendEventTx(ctx, tx, "bot_group.upsert", group)
		return err
	})
	if err == nil {
		s.options.Publisher.PublishCommitted([]store.Event{event})
	}
	return group, err
}
func validateGroup(g Group) error {
	if strings.TrimSpace(g.Name) == "" || utf8.RuneCountInString(g.Name) > 128 || utf8.RuneCountInString(g.Description) > 512 || g.MaxHops < 1 || g.MaxHops > 10 || len(g.Members) < 1 || len(g.Members) > 16 {
		return errors.New("Invalid group configuration")
	}
	seen := map[string]bool{}
	for _, m := range g.Members {
		if m.MachineID == "" || len(m.MachineID) > 128 || m.AgentID == "" || len(m.AgentID) > 256 || seen[m.AgentID] {
			return errors.New("Invalid or duplicate group member")
		}
		seen[m.AgentID] = true
		for _, item := range []struct {
			value *string
			limit int
		}{{m.Name, 128}, {m.Alias, 128}, {m.SystemRolePrompt, 2000}, {m.ModelProvider, 160}, {m.ModelName, 160}, {m.ThinkingEffort, 32}, {m.ApprovalPolicy, 32}, {m.Workspace, 2000}} {
			if item.value != nil && utf8.RuneCountInString(*item.value) > item.limit {
				return errors.New("Invalid member configuration")
			}
		}
	}
	return nil
}
func applyFields(g *Group, payload map[string]json.RawMessage) error {
	for key, raw := range payload {
		var destination any
		switch key {
		case "name":
			destination = &g.Name
		case "description":
			if string(raw) == "null" {
				continue
			}
			destination = &g.Description
		case "max_hops":
			destination = &g.MaxHops
		case "members":
			destination = &g.Members
		default:
			continue
		}
		if err := json.Unmarshal(raw, destination); err != nil {
			return errors.New("Invalid request")
		}
	}
	g.Name = strings.TrimSpace(g.Name)
	g.Description = strings.TrimSpace(g.Description)
	return validateGroup(*g)
}
func (s *Service) create(ctx context.Context, payload map[string]json.RawMessage) (Group, error) {
	id, err := randomID("group-")
	if err != nil {
		return Group{}, err
	}
	g := Group{ID: id, MaxHops: 3, ActiveSpeakers: []string{}, MemberStates: map[string]MemberState{}, CreatedAt: now(), UpdatedAt: now()}
	if err = applyFields(&g, payload); err != nil {
		return Group{}, err
	}
	for _, m := range g.Members {
		g.MemberStates[m.AgentID] = MemberState{Status: "idle", UpdatedAt: now()}
	}
	var event store.Event
	err = s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := putTx(ctx, tx, "bot_group:"+id, g); err != nil {
			return err
		}
		var err error
		event, err = appendEventTx(ctx, tx, "bot_group.upsert", g)
		return err
	})
	if err == nil {
		s.options.Publisher.PublishCommitted([]store.Event{event})
	}
	return g, err
}
func (s *Service) update(ctx context.Context, id string, payload map[string]json.RawMessage) (Group, error) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active[id]) > 0 {
		return Group{}, &serviceError{409, errors.New("群聊正在运行，请先停止")}
	}
	return s.mutate(ctx, id, func(g *Group) error {
		if err := applyFields(g, payload); err != nil {
			return err
		}
		states := map[string]MemberState{}
		for _, m := range g.Members {
			state, ok := g.MemberStates[m.AgentID]
			if !ok {
				state = MemberState{Status: "idle", UpdatedAt: now()}
			}
			states[m.AgentID] = state
		}
		g.MemberStates = states
		return nil
	})
}
func (s *Service) remove(ctx context.Context, id string) error {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	if _, err := s.get(ctx, id); err == nil {
		if _, err = s.stopLocked(ctx, id); err != nil {
			return err
		}
	} else if !errors.Is(err, errMissing) {
		return err
	}
	var event store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, key := range []string{"bot_group:" + id, "bot_group_msgs:" + id} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=?`, key); err != nil {
				return err
			}
		}
		for _, prefix := range []string{"blackboard:group:" + id + ":", "group_session:" + id + ":"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE substr(key,1,?)=?`, len(prefix), prefix); err != nil {
				return err
			}
		}
		var err error
		event, err = appendEventTx(ctx, tx, "bot_group.delete", map[string]any{"id": id})
		return err
	})
	if err == nil {
		s.options.Publisher.PublishCommitted([]store.Event{event})
	}
	return err
}

// Mentions preserve first occurrence order and support member aliases.
func Mentions(text string, members []Member) []string {
	lower := strings.ToLower(text)
	result := []string{}
	if strings.Contains(lower, "@all") || strings.Contains(lower, "@所有人") {
		for _, m := range members {
			result = append(result, m.AgentID)
		}
		return result
	}
	type match struct {
		pos   int
		agent string
	}
	matches := []match{}
	for _, m := range members {
		patterns := []string{m.AgentID}
		if m.Name != nil {
			patterns = append(patterns, *m.Name)
			for _, part := range strings.FieldsFunc(*m.Name, func(r rune) bool { return r == ' ' || r == '·' || r == '-' || r == '_' }) {
				if utf8.RuneCountInString(part) >= 2 {
					patterns = append(patterns, part)
				}
			}
		}
		if m.Alias != nil {
			patterns = append(patterns, *m.Alias)
		}
		position := -1
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			if at := strings.Index(lower, "@"+strings.ToLower(pattern)); at >= 0 && (position < 0 || at < position) {
				position = at
			}
		}
		if position >= 0 {
			matches = append(matches, match{position, m.AgentID})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })
	for _, m := range matches {
		result = append(result, m.agent)
	}
	return result
}
func (s *Service) appendMessage(ctx context.Context, message Message) error {
	var event store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := readGroupTx(ctx, tx, message.GroupID); err != nil {
			return err
		}
		var raw string
		messages := []Message{}
		err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, "bot_group_msgs:"+message.GroupID).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &messages); err != nil {
				return err
			}
		}
		messages = append(messages, message)
		if err = putTx(ctx, tx, "bot_group_msgs:"+message.GroupID, messages); err != nil {
			return err
		}
		event, err = appendEventTx(ctx, tx, "bot_group.message", message)
		return err
	})
	if err == nil {
		s.options.Publisher.PublishCommitted([]store.Event{event})
	}
	return err
}
func (s *Service) send(ctx context.Context, id string, payload map[string]json.RawMessage) (any, error) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, &serviceError{503, errors.New("群聊服务正在关闭")}
	}
	g, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	var text, target string
	var mentions []string
	if err = json.Unmarshal(payload["text"], &text); err != nil || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 32000 {
		return nil, errors.New("Invalid group message")
	}
	if raw, ok := payload["target_agent_id"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &target); err != nil {
			return nil, errors.New("Invalid target")
		}
	}
	if raw, ok := payload["mentions"]; ok {
		if err = json.Unmarshal(raw, &mentions); err != nil {
			return nil, errors.New("Invalid mentions")
		}
	}
	if len(mentions) == 0 {
		mentions = Mentions(text, g.Members)
	}
	targets := mentions
	if target != "" {
		targets = []string{target}
	}
	if len(targets) == 0 {
		for _, m := range g.Members {
			targets = append(targets, m.AgentID)
		}
	}
	members := map[string]Member{}
	for _, m := range g.Members {
		members[m.AgentID] = m
	}
	for _, id := range targets {
		if _, ok := members[id]; !ok {
			return nil, errors.New("目标不在群聊成员中")
		}
		if state := g.MemberStates[id]; state.Status == "running" || state.Status == "unknown" {
			return nil, &serviceError{409, errors.New("成员仍有执行中或未确认结果，请先停止或核对")}
		}
		s.mu.Lock()
		busy := s.active[g.ID][id] != nil
		s.mu.Unlock()
		if busy {
			return nil, &serviceError{409, errors.New("成员正在运行")}
		}
	}
	messageID, err := randomID("gmsg-")
	if err != nil {
		return nil, err
	}
	message := Message{ID: messageID, GroupID: id, SenderType: "user", SenderID: "human", SenderName: "用户", Text: strings.TrimSpace(text), Mentions: mentions, CreatedAt: now()}
	if err = s.appendMessage(ctx, message); err != nil {
		return nil, err
	}
	for _, target := range targets {
		s.dispatch(id, members[target], text, 1)
	}
	history, err := s.messages(ctx, id)
	if err != nil {
		return nil, err
	}
	var first any
	if len(targets) > 0 {
		first = targets[0]
	}
	return map[string]any{"ok": true, "message": message, "target_agent_id": first, "history_count": len(history)}, nil
}
func (s *Service) dispatch(id string, member Member, text string, hop int) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.active[id] == nil {
		s.active[id] = map[string]*activeTurn{}
	}
	if s.active[id][member.AgentID] != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 120*time.Second)
	commandID, err := randomID("cmd-g-")
	if err != nil {
		cancel()
		s.mu.Unlock()
		return
	}
	run := &activeTurn{turn: Turn{GroupID: id, Member: member, Prompt: text, Hop: hop, CommandID: commandID}, cancel: cancel}
	s.active[id][member.AgentID] = run
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		turn := run.turn
		g, err := s.mutate(ctx, id, func(g *Group) error {
			if hop > g.MaxHops {
				return errors.New("群聊已达到最大转交次数")
			}
			g.ActiveHop = max(g.ActiveHop, hop)
			g.ActiveSpeakers = append(g.ActiveSpeakers, member.AgentID)
			g.ActiveSpeaker = &member.AgentID
			g.MemberStates[member.AgentID] = MemberState{Status: "running", UpdatedAt: now()}
			return nil
		})
		if err == nil {
			history, readErr := s.messages(ctx, id)
			if readErr != nil {
				err = readErr
			} else {
				lines := []string{fmt.Sprintf("你正在参与星序多 Agent 协作群「%s」。", g.Name), "直截了当地回复与解决问题。需要交接时明确 @成员名称，完成后返回简短结果。", fmt.Sprintf("共享黑板 session_key=group::%s", id)}
				if member.SystemRolePrompt != nil {
					lines = append(lines, "你的群内职责："+*member.SystemRolePrompt)
				}
				for _, m := range g.Members {
					name := m.AgentID
					if m.Name != nil {
						name = *m.Name
					}
					lines = append(lines, "- @"+name)
				}
				for _, message := range history[max(0, len(history)-6):] {
					lines = append(lines, "["+message.SenderName+"]: "+message.Text)
				}
				lines = append(lines, "当前任务："+text)
				turn.Prompt = strings.Join(lines, "\n")
			}
		}
		var reply string
		if err == nil {
			reply, err = s.options.Controller.Run(ctx, turn, func(session string) error {
				s.mu.Lock()
				run.turn.SessionID = session
				stopped := run.explicitStop
				boundTurn := run.turn
				s.mu.Unlock()
				if stopped {
					interruptContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					if err := s.options.Controller.Interrupt(interruptContext, boundTurn); err != nil {
						return err
					}
					return context.Canceled
				}
				_, err := s.mutate(ctx, id, func(g *Group) error {
					state := g.MemberStates[member.AgentID]
					state.SessionID = session
					g.MemberStates[member.AgentID] = state
					return nil
				})
				return err
			})
		}
		s.submissionMu.Lock()
		defer s.submissionMu.Unlock()
		s.mu.Lock()
		current := s.active[id][member.AgentID] == run
		sessionID := run.turn.SessionID
		s.mu.Unlock()
		if !current || s.ctx.Err() != nil {
			return
		}
		state := MemberState{Status: "success", SessionID: sessionID, UpdatedAt: now()}
		if err != nil {
			state.Status = "failed"
			state.LastError = err.Error()
			reply = "执行失败：" + err.Error()
		}
		messageID, idErr := randomID("gmsg-")
		if idErr == nil {
			name := member.AgentID
			if member.Name != nil {
				name = *member.Name
			}
			message := Message{ID: messageID, GroupID: id, SenderType: "agent", SenderID: member.AgentID, SenderName: name, Text: reply, Mentions: Mentions(reply, g.Members), HopCount: hop, CreatedAt: now()}
			if message.Text != "" {
				if appendErr := s.appendMessage(s.ctx, message); appendErr != nil {
					state.Status = "failed"
					state.LastError = appendErr.Error()
				}
			}
		}
		_, updateErr := s.mutate(s.ctx, id, func(g *Group) error {
			g.MemberStates[member.AgentID] = state
			remaining := []string{}
			for _, id := range g.ActiveSpeakers {
				if id != member.AgentID {
					remaining = append(remaining, id)
				}
			}
			g.ActiveSpeakers = remaining
			g.ActiveSpeaker = nil
			if len(remaining) == 0 {
				g.ActiveHop = 0
			} else {
				g.ActiveSpeaker = &remaining[0]
			}
			return nil
		})
		s.mu.Lock()
		if s.active[id][member.AgentID] == run {
			delete(s.active[id], member.AgentID)
		}
		s.mu.Unlock()
		if updateErr != nil || err != nil || hop >= g.MaxHops {
			return
		}
		for _, target := range Mentions(reply, g.Members) {
			if target != member.AgentID {
				for _, m := range g.Members {
					if m.AgentID == target {
						s.dispatch(id, m, reply, hop+1)
					}
				}
			}
		}
	}()
}
func (s *Service) stop(ctx context.Context, id string) (Group, error) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	return s.stopLocked(ctx, id)
}
func (s *Service) stopLocked(ctx context.Context, id string) (Group, error) {
	s.mu.Lock()
	runs := s.active[id]
	delete(s.active, id)
	turns := []Turn{}
	for _, run := range runs {
		run.explicitStop = true
		run.cancel()
		turns = append(turns, run.turn)
	}
	s.mu.Unlock()
	group, readErr := s.get(ctx, id)
	if readErr != nil {
		return Group{}, readErr
	}
	for _, member := range group.Members {
		state := group.MemberStates[member.AgentID]
		if (state.Status == "running" || state.Status == "unknown") && state.SessionID != "" {
			found := false
			for _, turn := range turns {
				if turn.Member.AgentID == member.AgentID {
					found = true
				}
			}
			if !found {
				commandID, err := randomID("cmd-g-stop-")
				if err != nil {
					return Group{}, err
				}
				turns = append(turns, Turn{GroupID: id, Member: member, SessionID: state.SessionID, CommandID: commandID})
			}
		}
	}
	failures := map[string]string{}
	for _, turn := range turns {
		if turn.SessionID != "" {
			interruptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.options.Controller.Interrupt(interruptCtx, turn)
			cancel()
			if err != nil {
				failures[turn.Member.AgentID] = err.Error()
			}
		}
	}
	g, err := s.mutate(ctx, id, func(g *Group) error {
		g.ActiveHop = 0
		g.ActiveSpeaker = nil
		g.ActiveSpeakers = []string{}
		g.StoppedAt = now()
		for _, turn := range turns {
			state := g.MemberStates[turn.Member.AgentID]
			state.Status = "cancelled"
			state.UpdatedAt = now()
			if detail := failures[turn.Member.AgentID]; detail != "" {
				state.Status = "failed"
				state.LastError = detail
			}
			g.MemberStates[turn.Member.AgentID] = state
		}
		return nil
	})
	if err != nil {
		return g, err
	}
	if len(failures) > 0 {
		return g, &serviceError{502, errors.New("部分成员未确认停止；具体错误已保存在成员状态")}
	}
	return g, nil
}
