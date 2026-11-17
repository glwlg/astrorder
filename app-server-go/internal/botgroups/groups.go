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
	"unicode"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

type Member struct {
	ID               string  `json:"id,omitempty"`
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
	SourceSessionID  *string `json:"source_session_id,omitempty"`
	ForkSessionID    *string `json:"fork_session_id,omitempty"`
}
type MemberState struct {
	Status    string `json:"status"`
	Muted     bool   `json:"muted,omitempty"`
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
	Coordination   *Coordination          `json:"coordination,omitempty"`
}
type Message struct {
	ID             string           `json:"id"`
	GroupID        string           `json:"group_id"`
	SenderType     string           `json:"sender_type"`
	SenderID       string           `json:"sender_id"`
	SenderMemberID string           `json:"sender_member_id,omitempty"`
	SenderName     string           `json:"sender_name"`
	Text           string           `json:"text"`
	Mentions       []string         `json:"mentions"`
	HopCount       int              `json:"hop_count"`
	CreatedAt      string           `json:"created_at"`
	SessionID      string           `json:"session_id,omitempty"`
	NativeIDs      []string         `json:"native_ids,omitempty"`
	Attachments    []map[string]any `json:"attachments,omitempty"`
}
type Reply struct {
	Text      string
	NativeIDs []string
	CreatedAt string
}
type Turn struct {
	GroupID     string
	Member      Member
	Prompt      string
	Hop         int
	CommandID   string
	SessionID   string
	ThroughID   string
	Attachments []map[string]any
	// RecordBaseline persists native message IDs before the command is sent.
	RecordBaseline func([]string) error
	// RecordAccepted advances the cursor only after the native send is confirmed.
	RecordAccepted func() error
}

// Controller binds one exact session and returns a completed native reply.
// Interrupt is called only by an explicit group stop or deletion.
type Controller interface {
	Run(context.Context, Turn, func(string) error) (Reply, error)
	Interrupt(context.Context, Turn) error
}

type replyReader interface {
	ReadReply(context.Context, string, string, []string) (string, error)
}
type Publisher interface{ PublishCommitted([]store.Event) }
type Options struct {
	Store           *store.Store
	Controller      Controller
	Authorizer      *auth.Authenticator
	Publisher       Publisher
	Decider         Decider
	FileResolver    ArtifactFileResolver
	ArtifactDecider ArtifactDecider
}
type activeTurn struct {
	turn         Turn
	cancel       context.CancelFunc
	explicitStop bool
}
type Service struct {
	options           Options
	ctx               context.Context
	cancel            context.CancelFunc
	mu                sync.Mutex
	submissionMu      sync.Mutex
	active            map[string]map[string]*activeTurn
	scheduling        map[string]bool
	reschedule        map[string]bool
	wg                sync.WaitGroup
	closed            bool
	artifactWorkers   map[string]bool
	artifactRequested map[string]bool
}

var errMissing = errors.New("群聊未找到")

func New(ctx context.Context, options Options) (*Service, error) {
	if options.Store == nil || options.Controller == nil || options.Authorizer == nil || options.Publisher == nil {
		return nil, errors.New("group store, controller, authentication and event publisher are required")
	}
	if options.Decider == nil {
		options.Decider = &systemOneDecider{store: options.Store}
	}
	if options.ArtifactDecider == nil {
		options.ArtifactDecider = &systemOneArtifactDecider{store: options.Store}
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{options: options, ctx: ctx, cancel: cancel, active: make(map[string]map[string]*activeTurn), scheduling: make(map[string]bool), reschedule: make(map[string]bool)}
	groups, err := s.list(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, g := range groups {
		if err := s.initializeFileIndex(ctx, g.ID); err != nil {
			cancel()
			return nil, err
		}
		s.scheduleArtifacts(g.ID)
		type recovery struct {
			member   Member
			session  string
			delivery Delivery
		}
		recoverable := []recovery{}
		unknown := map[string]bool{}
		_, canResume := options.Controller.(recoveryController)
		for _, member := range g.Members {
			mid := memberID(member)
			state := g.MemberStates[mid]
			if state.Status != "running" {
				continue
			}
			var delivery Delivery
			if g.Coordination != nil {
				delivery = g.Coordination.InFlight[mid]
			}
			if canResume && state.SessionID != "" && delivery.CommandID != "" && delivery.BaselineCaptured {
				recoverable = append(recoverable, recovery{member, state.SessionID, delivery})
			} else {
				unknown[mid] = true
			}
		}
		if len(unknown) > 0 {
			if _, err = s.mutate(ctx, g.ID, func(group *Group) error {
				for id, state := range group.MemberStates {
					if unknown[id] {
						state.Status = "unknown"
						state.LastError = "App 已重新连接，原生执行结果未确认；不会自动重发。"
						state.UpdatedAt = now()
						group.MemberStates[id] = state
					}
				}
				speakers := make([]string, 0, len(group.ActiveSpeakers))
				for _, mid := range group.ActiveSpeakers {
					if !unknown[mid] {
						speakers = append(speakers, mid)
					}
				}
				group.ActiveSpeakers = speakers
				group.ActiveSpeaker = nil
				if len(speakers) > 0 {
					group.ActiveSpeaker = &speakers[0]
				} else {
					group.ActiveHop = 0
				}
				if group.Coordination != nil {
					group.Coordination.Status = "error"
					group.Coordination.Error = "成员的执行结果尚未确认，请核对会话"
					group.Coordination.Revision++
				}
				return nil
			}); err != nil {
				cancel()
				return nil, err
			}
		}
		for _, item := range recoverable {
			s.resume(g.ID, item.member, item.session, item.delivery)
		}
		if g.Coordination != nil && len(unknown) == 0 && (len(g.Coordination.Pending) > 0 || g.Coordination.Status == "deciding") {
			s.schedule(g.ID)
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
	if parts[2] == "artifacts" {
		return len(parts) == 3 && (method == http.MethodGet || method == http.MethodPost)
	}
	return len(parts) == 3 && ((parts[2] == "messages" && (method == http.MethodGet || method == http.MethodPost)) || ((parts[2] == "stop" || parts[2] == "retry" || parts[2] == "recover") && method == http.MethodPost))
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
		case len(parts) == 3 && parts[2] == "artifacts" && r.Method == http.MethodPost:
			result, err = s.recheckArtifacts(r.Context(), id)
		case len(parts) == 3 && parts[2] == "artifacts":
			result, err = s.artifactDirectory(r.Context(), id)
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
		case parts[2] == "retry":
			var group Group
			group, err = s.retryDecision(r.Context(), id)
			result = map[string]any{"ok": err == nil, "group": group}
		case parts[2] == "recover":
			var group Group
			group, err = s.recoverTimedOut(r.Context(), id)
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
func memberID(member Member) string {
	if member.ID != "" {
		return member.ID
	}
	return member.AgentID
}
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
	messages, err := s.storedMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	resolved := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.SenderType == "agent" {
			// Legacy copied replies are no longer a history source.
			if message.SessionID == "" || len(message.NativeIDs) == 0 {
				continue
			}
			reader, ok := s.options.Controller.(replyReader)
			if !ok {
				return nil, errors.New("native group history is unavailable")
			}
			message.Text, err = reader.ReadReply(ctx, message.SenderID, message.SessionID, message.NativeIDs)
			if err != nil {
				return nil, fmt.Errorf("读取成员「%s」的群聊回复失败：%w", message.SenderName, err)
			}
		}
		resolved = append(resolved, message)
	}
	s.scheduleArtifacts(id)
	return resolved, nil
}

func (s *Service) storedMessages(ctx context.Context, id string) ([]Message, error) {
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
	if store.IsTransientEvent(kind) {
		return store.Event{ID: id, Type: kind, Data: data}, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO events(event_id,type,agent_id,session_id,data,created_at)VALUES(?,?,NULL,NULL,?,?)`, id, kind, string(body), now())
	if err != nil {
		return store.Event{}, err
	}
	cursor, err := result.LastInsertId()
	return store.Event{ID: id, Cursor: cursor, Type: kind, Data: data}, err
}
func (s *Service) mutate(ctx context.Context, id string, fn func(*Group) error) (Group, error) {
	return s.mutateTx(ctx, id, func(group *Group, _ *sql.Tx) error { return fn(group) })
}
func (s *Service) mutateTx(ctx context.Context, id string, fn func(*Group, *sql.Tx) error) (Group, error) {
	var group Group
	var event store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		group, err = readGroupTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = fn(&group, tx); err != nil {
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
func validateGroup(g Group, previous map[string]Member) error {
	if strings.TrimSpace(g.Name) == "" || utf8.RuneCountInString(g.Name) > 128 || utf8.RuneCountInString(g.Description) > 512 || g.MaxHops < 1 || g.MaxHops > 20 || len(g.Members) < 1 || len(g.Members) > 16 {
		return errors.New("Invalid group configuration")
	}
	seen := map[string]bool{}
	aliases := map[string]bool{}
	agentCounts := map[string]int{}
	for _, member := range g.Members {
		agentCounts[member.AgentID]++
	}
	for _, m := range g.Members {
		id := memberID(m)
		if m.MachineID == "" || len(m.MachineID) > 128 || m.AgentID == "" || len(m.AgentID) > 256 || id == "" || len(id) > 256 || seen[id] {
			return errors.New("Invalid or duplicate group member")
		}
		seen[id] = true
		alias := ""
		if m.Alias != nil {
			alias = strings.TrimSpace(*m.Alias)
		}
		old, existed := previous[id]
		legacyAlias := existed && old.ID == "" && m.ID == "" && memberValue(old.Alias) == alias
		if alias == "" && agentCounts[m.AgentID] > 1 {
			return errors.New("同一 Agent 的多个成员需要设置不同的群内别名")
		}
		if alias != "" {
			key := strings.ToLower(alias)
			invalid := key == "all" || key == "所有人" || strings.ContainsFunc(alias, func(r rune) bool { return unicode.IsSpace(r) || r == '@' })
			otherLegacy, duplicate := aliases[key]
			if (invalid && (!legacyAlias || agentCounts[m.AgentID] > 1)) || (duplicate && (!legacyAlias || !otherLegacy)) {
				return errors.New("群内别名不能重复，也不能包含空格或 @")
			}
			aliases[key] = legacyAlias
		}
		if (m.SourceSessionID == nil) != (m.ForkSessionID == nil) {
			return errors.New("分叉成员需要同时提供来源会话和分叉会话")
		}
		for _, item := range []struct {
			value *string
			limit int
		}{{m.Name, 128}, {m.Alias, 128}, {m.SystemRolePrompt, 2000}, {m.ModelProvider, 160}, {m.ModelName, 160}, {m.ThinkingEffort, 32}, {m.ApprovalPolicy, 32}, {m.Workspace, 2000}, {m.SourceSessionID, 256}, {m.ForkSessionID, 256}} {
			if item.value != nil && utf8.RuneCountInString(*item.value) > item.limit {
				return errors.New("Invalid member configuration")
			}
		}
	}
	return nil
}
func applyFields(g *Group, payload map[string]json.RawMessage, previous map[string]Member) error {
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
			var members []Member
			if err := json.Unmarshal(raw, &members); err != nil {
				return errors.New("Invalid request")
			}
			g.Members = members
			continue
		default:
			continue
		}
		if err := json.Unmarshal(raw, destination); err != nil {
			return errors.New("Invalid request")
		}
	}
	g.Name = strings.TrimSpace(g.Name)
	g.Description = strings.TrimSpace(g.Description)
	return validateGroup(*g, previous)
}
func (s *Service) create(ctx context.Context, payload map[string]json.RawMessage) (Group, error) {
	id, err := randomID("group-")
	if err != nil {
		return Group{}, err
	}
	g := Group{ID: id, MaxHops: 10, ActiveSpeakers: []string{}, MemberStates: map[string]MemberState{}, CreatedAt: now(), UpdatedAt: now()}
	if err = applyFields(&g, payload, nil); err != nil {
		return Group{}, err
	}
	for _, m := range g.Members {
		g.MemberStates[memberID(m)] = MemberState{Status: "idle", UpdatedAt: now()}
	}
	var event store.Event
	err = s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, member := range g.Members {
			if err := bindForkTx(ctx, tx, id, member); err != nil {
				return err
			}
			if member.ForkSessionID != nil {
				state := g.MemberStates[memberID(member)]
				state.SessionID = *member.ForkSessionID
				g.MemberStates[memberID(member)] = state
			}
		}
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
	if raw, ok := payload["member_mutes"]; ok {
		if len(payload) != 1 {
			return Group{}, errors.New("禁言设置请单独提交")
		}
		return s.updateMemberMutes(ctx, id, raw)
	}
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active[id]) > 0 {
		return Group{}, &serviceError{409, errors.New("群聊正在运行，请先停止")}
	}
	return s.mutateTx(ctx, id, func(g *Group, tx *sql.Tx) error {
		if g.Coordination != nil && g.Coordination.Status == "deciding" {
			return &serviceError{409, errors.New("群聊正在判断，请稍后调整设置")}
		}
		for _, state := range g.MemberStates {
			if state.Status == "running" || state.Status == "unknown" {
				return &serviceError{409, errors.New("群聊仍有执行中或未确认的成员，请先停止或核对")}
			}
		}
		_, changesLimit := payload["max_hops"]
		// A paused round keeps its queue and cursors. Changing only its limit
		// must not require stopping the round or implicitly retry its delivery.
		if changesLimit && len(payload) == 1 && (g.Coordination == nil || len(g.Coordination.InFlight) == 0) {
			previous := make(map[string]Member, len(g.Members))
			for _, member := range g.Members {
				previous[memberID(member)] = member
			}
			if err := applyFields(g, payload, previous); err != nil {
				return err
			}
			if g.Coordination != nil {
				g.Coordination.Revision++
			}
			return nil
		}
		if g.Coordination != nil && (len(g.Coordination.Pending) > 0 || len(g.Coordination.InFlight) > 0) {
			return &serviceError{409, errors.New("群聊仍有待处理任务，当前只能单独调整接力上限；修改其他设置请先停止或核对")}
		}
		previous := make(map[string]Member, len(g.Members))
		for _, member := range g.Members {
			previous[memberID(member)] = member
		}
		if err := applyFields(g, payload, previous); err != nil {
			return err
		}
		states := map[string]MemberState{}
		for _, m := range g.Members {
			id := memberID(m)
			state, ok := g.MemberStates[id]
			if !ok {
				state = MemberState{Status: "idle", UpdatedAt: now()}
			}
			old, existed := previous[id]
			if existed {
				delete(previous, id)
				bound, err := groupBindingExistsTx(ctx, tx, g.ID, id)
				if err != nil {
					return err
				}
				if bound && !sameMemberSession(old, m) {
					return &serviceError{409, errors.New("已有会话的成员不能更换 Agent、工作区或会话来源，请移除后重新添加")}
				}
				if !bound && m.ForkSessionID != nil {
					if err := bindForkTx(ctx, tx, g.ID, m); err != nil {
						return err
					}
					state.SessionID = *m.ForkSessionID
				}
			} else {
				bound, err := groupBindingExistsTx(ctx, tx, g.ID, id)
				if err != nil {
					return err
				}
				if bound {
					return errors.New("群成员标识已被使用")
				}
				if err := bindForkTx(ctx, tx, g.ID, m); err != nil {
					return err
				}
				if m.ForkSessionID != nil {
					state.SessionID = *m.ForkSessionID
				}
			}
			states[id] = state
		}
		for id := range previous {
			if err := unbindMemberTx(ctx, tx, g.ID, id); err != nil {
				return err
			}
			if g.Coordination != nil {
				delete(g.Coordination.Delivered, id)
				delete(g.Coordination.SkippedThrough, id)
			}
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
		group, err := readGroupTx(ctx, tx, id)
		if err != nil && !errors.Is(err, errMissing) {
			return err
		}
		for _, member := range group.Members {
			if err := unbindMemberTx(ctx, tx, id, memberID(member)); err != nil {
				return err
			}
		}
		for _, key := range []string{"bot_group:" + id, "bot_group_msgs:" + id, "bot_group_artifacts_cursor:" + id, fileIndexKey(id), artifacts.DatabaseKey(id)} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=?`, key); err != nil {
				return err
			}
		}
		for _, prefix := range []string{"blackboard:group:" + id + ":", "group_session:" + id + ":"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE substr(key,1,?)=?`, len(prefix), prefix); err != nil {
				return err
			}
		}
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
	if mentionPosition(lower, "all") >= 0 || mentionPosition(lower, "所有人") >= 0 {
		for _, m := range members {
			result = append(result, memberID(m))
		}
		return result
	}
	type match struct {
		pos   int
		agent string
	}
	matches := []match{}
	aliasPositions := map[int]bool{}
	for _, m := range members {
		if m.Alias != nil && strings.TrimSpace(*m.Alias) != "" {
			if at := mentionPosition(lower, strings.ToLower(strings.TrimSpace(*m.Alias))); at >= 0 {
				aliasPositions[at] = true
			}
		}
	}
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
		position := -1
		if m.Alias != nil && strings.TrimSpace(*m.Alias) != "" {
			position = mentionPosition(lower, strings.ToLower(strings.TrimSpace(*m.Alias)))
		}
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			if at := mentionPosition(lower, strings.ToLower(pattern)); at >= 0 && !aliasPositions[at] && (position < 0 || at < position) {
				position = at
			}
		}
		if position >= 0 {
			matches = append(matches, match{position, memberID(m)})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })
	for _, m := range matches {
		result = append(result, m.agent)
	}
	return result
}
func mentionPosition(text, name string) int {
	needle := "@" + name
	offset := 0
	for offset < len(text) {
		at := strings.Index(text[offset:], needle)
		if at < 0 {
			return -1
		}
		at += offset
		end := at + len(needle)
		if end == len(text) {
			return at
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' && next != '-' {
			return at
		}
		offset = end
	}
	return -1
}
func appendMessageTx(ctx context.Context, tx *sql.Tx, message Message) ([]store.Event, int, error) {
	var raw string
	messages := []Message{}
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, "bot_group_msgs:"+message.GroupID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &messages); err != nil {
			return nil, 0, err
		}
	}
	persisted := message
	if persisted.SenderType == "agent" {
		persisted.Text = ""
	}
	messages = append(messages, persisted)
	if err = putTx(ctx, tx, "bot_group_msgs:"+message.GroupID, messages); err != nil {
		return nil, 0, err
	}
	event, err := appendEventTx(ctx, tx, "bot_group.message", message)
	if err != nil {
		return nil, 0, err
	}
	events := []store.Event{event}
	return events, len(messages), nil
}

func (s *Service) appendMessage(ctx context.Context, message Message) error {
	var events []store.Event
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := readGroupTx(ctx, tx, message.GroupID); err != nil {
			return err
		}
		var err error
		events, _, err = appendMessageTx(ctx, tx, message)
		return err
	})
	if err == nil {
		s.options.Publisher.PublishCommitted(events)
		s.scheduleArtifacts(message.GroupID)
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
	var text, targetAgent, targetMember string
	var mentions []string
	if raw, ok := payload["text"]; ok {
		if err = json.Unmarshal(raw, &text); err != nil {
			return nil, errors.New("Invalid group message")
		}
	}
	attachments, err := s.messageAttachments(ctx, payload["attachment_ids"])
	if err != nil {
		return nil, err
	}
	if (strings.TrimSpace(text) == "" && len(attachments) == 0) || utf8.RuneCountInString(text) > 32000 {
		return nil, errors.New("Invalid group message")
	}
	if raw, ok := payload["target_agent_id"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &targetAgent); err != nil {
			return nil, errors.New("Invalid target")
		}
	}
	if raw, ok := payload["target_member_id"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &targetMember); err != nil {
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
	if targetMember != "" {
		targets = []string{targetMember}
	} else if targetAgent != "" {
		targets = nil
		for _, member := range g.Members {
			if member.AgentID == targetAgent {
				targets = append(targets, memberID(member))
			}
		}
		if len(targets) != 1 {
			return nil, errors.New("请指定明确的群成员")
		}
	}
	if len(targets) == 0 {
		for _, m := range g.Members {
			targets = append(targets, memberID(m))
		}
	}
	members := map[string]Member{}
	for _, m := range g.Members {
		members[memberID(m)] = m
	}
	uniqueTargets := make([]string, 0, len(targets))
	seenTargets := map[string]bool{}
	for _, id := range targets {
		if _, ok := members[id]; !ok {
			return nil, errors.New("目标不在群聊成员中")
		}
		if seenTargets[id] {
			continue
		}
		seenTargets[id] = true
		if g.MemberStates[id].Muted {
			continue
		}
		uniqueTargets = append(uniqueTargets, id)
		if state := g.MemberStates[id]; state.Status == "unknown" {
			return nil, &serviceError{409, errors.New("成员仍有未确认的执行结果，请先核对")}
		}
	}
	targets = uniqueTargets
	messageID, err := randomID("gmsg-")
	if err != nil {
		return nil, err
	}
	message := Message{ID: messageID, GroupID: id, SenderType: "user", SenderID: "human", SenderName: "用户", Text: strings.TrimSpace(text), Mentions: mentions, Attachments: attachments, CreatedAt: now()}
	history, err := s.messages(ctx, id)
	if err != nil {
		return nil, err
	}
	historyCount, err := s.queueUserMessage(ctx, id, message, targets, history)
	if err != nil {
		return nil, err
	}
	s.schedule(id)
	var firstMember, firstAgent any
	if len(targets) > 0 {
		firstMember = targets[0]
		firstAgent = members[targets[0]].AgentID
	}
	return map[string]any{"ok": true, "message": message, "target_agent_id": firstAgent, "target_member_id": firstMember, "history_count": historyCount}, nil
}
func (s *Service) dispatch(id string, member Member, text string, hop int, throughID, commandID string, attachments []map[string]any) {
	mid := memberID(member)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.active[id] == nil {
		s.active[id] = map[string]*activeTurn{}
	}
	if s.active[id][mid] != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	run := &activeTurn{turn: Turn{GroupID: id, Member: member, Prompt: text, Hop: hop, CommandID: commandID, ThroughID: throughID, Attachments: attachments}, cancel: cancel}
	s.active[id][mid] = run
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		turn := run.turn
		turn.RecordBaseline = func(ids []string) error {
			_, err := s.mutate(ctx, id, func(g *Group) error {
				if g.Coordination == nil {
					return errors.New("群聊任务状态不存在")
				}
				delivery, ok := g.Coordination.InFlight[mid]
				if !ok || delivery.CommandID != commandID {
					return errors.New("群聊任务已变化")
				}
				delivery.BaselineCaptured = true
				delivery.BaselineIDs = append([]string(nil), ids...)
				g.Coordination.InFlight[mid] = delivery
				return nil
			})
			return err
		}
		turn.RecordAccepted = func() error {
			return s.recordAccepted(ctx, id, mid, commandID)
		}
		reply, err := s.options.Controller.Run(ctx, turn, func(session string) error {
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
				state := g.MemberStates[mid]
				state.SessionID = session
				g.MemberStates[mid] = state
				return nil
			})
			return err
		})
		s.finishDelivery(id, member, run, reply, err)
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
		mid := memberID(member)
		state := group.MemberStates[mid]
		if (state.Status == "running" || state.Status == "unknown") && state.SessionID != "" {
			found := false
			for _, turn := range turns {
				if memberID(turn.Member) == mid {
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
				failures[memberID(turn.Member)] = err.Error()
			}
		}
	}
	g, err := s.mutate(ctx, id, func(g *Group) error {
		g.ActiveHop = 0
		g.ActiveSpeaker = nil
		g.ActiveSpeakers = []string{}
		g.StoppedAt = now()
		if g.Coordination != nil {
			flow := ensureCoordination(g, nil)
			flow.Status = "stopped"
			flow.Error = ""
			flow.Pending = make(map[string]PendingDelivery)
			for _, turn := range turns {
				mid := memberID(turn.Member)
				if failures[mid] != "" {
					flow.Status = "error"
					flow.Error = "部分成员未确认停止，请核对会话"
					continue
				}
				if delivery, ok := flow.InFlight[mid]; ok {
					flow.Delivered[mid] = delivery.ThroughID
					delete(flow.InFlight, mid)
				}
			}
			flow.Revision++
		}
		for _, turn := range turns {
			mid := memberID(turn.Member)
			state := g.MemberStates[mid]
			state.Status = "cancelled"
			state.UpdatedAt = now()
			if detail := failures[mid]; detail != "" {
				state.Status = "unknown"
				state.LastError = detail
			}
			g.MemberStates[mid] = state
		}
		for mid, state := range g.MemberStates {
			if state.Status == "waiting" {
				state.Status = "cancelled"
				state.UpdatedAt = now()
				g.MemberStates[mid] = state
			}
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
