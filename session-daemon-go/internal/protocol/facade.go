package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"astrorder.dev/session-daemon/internal/events"
	core "astrorder.dev/session-daemon/internal/runtime"
)

const MaxBusinessBytes = 16_000_000

// AuthorityStatus is a transport-independent, durable authority snapshot.
// A failed read never falls back to the journal's in-memory status cache.
type AuthorityStatus struct {
	DaemonID          string
	Runtimes          []RuntimeAuthority
	Sessions          []SessionAuthority
	ConnectorIDs      []string
	PendingOperations uint64
	ModelConfigError  string
}

type RuntimeAuthority struct {
	Kind          string
	Registered    bool
	OwnedSessions uint32
	Busy          bool
}

type SessionAuthority struct {
	SessionID string
	Status    string
	MinSeqID  int64
	MaxSeqID  int64
	// RuntimeOwned is true when the session is currently bound to a live
	// runtime in this daemon's registry (with RuntimeKind and Identity).
	// Journal-only rows keep the durable seq bounds but are not live-owned.
	RuntimeOwned bool
	// RuntimeKind is the owning registered runtime kind; empty when the
	// session is not live-owned.
	RuntimeKind string
	// Identity is the immutable binding identity of the live owner. A
	// journal-only row never borrows another session's or connection's
	// identity.
	Identity core.SessionReadIdentity
}

type ShutdownDecision struct {
	DaemonID string
	Accepted bool
}

type EventInput struct {
	SessionID       string
	Event           string
	Status          string
	Payload         map[string]any
	TimestampMicros *int64
}

type ReplayCursor struct {
	SessionID string
	After     int64
}

type ReplayPlan struct {
	DaemonID     string
	Subscription *events.Subscription
	Sessions     map[string]events.SnapshotSession
	Cursors      map[string]int64
}

func (d *Daemon) DaemonID() string {
	return d.daemonID
}

func (d *Daemon) AuthorityStatus() (AuthorityStatus, error) {
	begin, err := d.journal.BeginSubscription(1)
	if err != nil {
		return AuthorityStatus{}, fmt.Errorf("authoritative event storage unavailable")
	}
	d.journal.Unsubscribe(begin.Subscription)
	pending, err := d.journal.PendingOperations()
	if err != nil {
		return AuthorityStatus{}, fmt.Errorf("authoritative operation storage unavailable")
	}

	d.mu.Lock()
	connectorIDs := make([]string, 0, len(d.connectors))
	for id := range d.connectors {
		connectorIDs = append(connectorIDs, id)
	}
	modelConfigError := d.modelReloadError
	d.mu.Unlock()
	sort.Strings(connectorIDs)

	runtimeStates := d.registry.Statuses()
	runtimeKinds := make([]string, 0, len(runtimeStates))
	for kind := range runtimeStates {
		runtimeKinds = append(runtimeKinds, kind)
	}
	sort.Strings(runtimeKinds)
	runtimes := make([]RuntimeAuthority, 0, len(runtimeKinds))
	for _, kind := range runtimeKinds {
		state := runtimeStates[kind]
		sessions, _ := state["sessions"].(int)
		busy, _ := state["busy"].(bool)
		runtimes = append(runtimes, RuntimeAuthority{Kind: kind, Registered: state["registered"] == true, OwnedSessions: uint32(sessions), Busy: busy})
	}

	sessions := make(map[string]SessionAuthority, len(begin.Sessions))
	for id, state := range begin.Sessions {
		sessions[id] = SessionAuthority{SessionID: id, Status: state.Status, MinSeqID: state.MinSeqID, MaxSeqID: state.MaxSeqID}
	}
	// Journal rows keep their durable seq bounds, but only a session that is
	// currently bound to a live runtime may carry ownership: kind, identity,
	// and the runtime_owned verdict. Journal history must not be able to
	// claim a live attachment, and it never borrows another session's kind or
	// identity.
	for id, binding := range d.registry.LiveSessionBindings() {
		current, exists := sessions[id]
		if !exists {
			current = SessionAuthority{SessionID: id}
		}
		current.Status = binding.State.Status
		current.RuntimeOwned = true
		current.RuntimeKind = binding.Kind
		current.Identity = binding.Identity
		sessions[id] = current
	}
	sessionIDs := make([]string, 0, len(sessions))
	for id := range sessions {
		sessionIDs = append(sessionIDs, id)
	}
	sort.Strings(sessionIDs)
	sessionList := make([]SessionAuthority, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		sessionList = append(sessionList, sessions[id])
	}
	return AuthorityStatus{
		DaemonID:          d.daemonID,
		Runtimes:          runtimes,
		Sessions:          sessionList,
		ConnectorIDs:      connectorIDs,
		PendingOperations: uint64(pending),
		ModelConfigError:  modelConfigError,
	}, nil
}

func (d *Daemon) AuthorizeShutdown(expectedDaemonID *string, confirmActive bool) (decision ShutdownDecision, resultErr error) {
	if expectedDaemonID != nil && *expectedDaemonID != d.daemonID {
		return ShutdownDecision{}, fmt.Errorf("daemon identity changed; shutdown refused")
	}
	d.admissionMu.Lock()
	defer d.admissionMu.Unlock()
	if d.shutdownPending || !confirmActive && d.activeControls > 0 {
		return ShutdownDecision{}, fmt.Errorf("daemon control admission is busy")
	}
	if err := d.registry.BeginShutdown(confirmActive); err != nil {
		return ShutdownDecision{}, fmt.Errorf("daemon has active runtime operations; explicit confirmation is required")
	}
	d.shutdownPending = true
	defer func() {
		if resultErr != nil {
			d.shutdownPending = false
			d.registry.CancelShutdown()
		}
	}()
	pending, err := d.journal.PendingOperations()
	if !confirmActive && (err != nil || pending > 0) {
		return ShutdownDecision{}, fmt.Errorf("daemon has pending or uncertain durable operations; explicit confirmation is required")
	}
	if d.registry.Busy() && !confirmActive {
		return ShutdownDecision{}, fmt.Errorf("daemon has active runtime operations; explicit confirmation is required")
	}
	begin, err := d.journal.BeginSubscription(1)
	if err != nil {
		if !confirmActive {
			return ShutdownDecision{}, fmt.Errorf("daemon authority is incomplete; explicit confirmation is required")
		}
		return ShutdownDecision{}, fmt.Errorf("daemon authority is incomplete")
	}
	d.journal.Unsubscribe(begin.Subscription)
	if !confirmActive {
		for _, state := range begin.Sessions {
			if state.Status != "idle" {
				return ShutdownDecision{}, fmt.Errorf("daemon has active sessions; explicit confirmation is required")
			}
		}
	}
	return ShutdownDecision{DaemonID: d.daemonID, Accepted: true}, nil
}

// SignalShutdown is called by a transport only after its accepted response has
// been handed to the transport. Refused requests never reach this method.
func (d *Daemon) SignalShutdown() {
	d.shutdownOnce.Do(func() { close(d.shutdownRequested) })
}

func (d *Daemon) AppendEvent(input EventInput) (events.Frame, error) {
	if err := d.beginControl(); err != nil {
		return events.Frame{}, err
	}
	defer d.endControl()
	if input.SessionID == "" || len(input.SessionID) > 256 || input.Event == "" || input.Status == "" || !validFacadeStatus(input.Status) {
		return events.Frame{}, fmt.Errorf("session event is invalid")
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.TimestampMicros == nil {
		return d.journal.Commit(input.SessionID, input.Event, input.Payload, input.Status)
	}
	return d.journal.CommitAt(input.SessionID, input.Event, input.Payload, input.Status, float64(*input.TimestampMicros)/1e6)
}

func (d *Daemon) BeginReplay(expectedDaemonID *string, cursors []ReplayCursor, capacity int) (ReplayPlan, error) {
	if expectedDaemonID != nil && *expectedDaemonID != d.daemonID {
		return ReplayPlan{DaemonID: d.daemonID}, fmt.Errorf("daemon epoch changed")
	}
	parsed := make(map[string]int64, len(cursors))
	for _, cursor := range cursors {
		if cursor.SessionID == "" || len(cursor.SessionID) > 256 || cursor.After < 0 {
			return ReplayPlan{}, fmt.Errorf("invalid replay cursor")
		}
		if _, exists := parsed[cursor.SessionID]; exists {
			return ReplayPlan{}, fmt.Errorf("duplicate replay cursor")
		}
		parsed[cursor.SessionID] = cursor.After
	}
	begin, err := d.journal.BeginSubscription(capacity)
	if err != nil {
		return ReplayPlan{}, err
	}
	return ReplayPlan{DaemonID: d.daemonID, Subscription: begin.Subscription, Sessions: begin.Sessions, Cursors: parsed}, nil
}

func (d *Daemon) EndReplay(plan ReplayPlan) {
	if plan.Subscription != nil {
		d.journal.Unsubscribe(plan.Subscription)
	}
}

func (d *Daemon) ReplayPage(sessionID string, after, through int64, limit int) (events.ReplayPage, error) {
	return d.journal.Page(sessionID, after, through, limit)
}

func DecodeNativeJSON(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > MaxBusinessBytes {
		return nil, fmt.Errorf("native JSON business payload is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, fmt.Errorf("native JSON payload must be an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("native JSON payload must contain exactly one value")
	}
	return payload, nil
}

func validFacadeStatus(status string) bool {
	return status == "idle" || status == "running" || status == "waiting_approval" || status == "error"
}
