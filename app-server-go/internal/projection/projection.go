package projection

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/notifications"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var projectionCounter atomic.Int64

// A live gap may be repaired by a fresh subscription using only durable
// checkpoints. Replay gaps remain fatal because the authoritative replay itself
// must be contiguous.
type liveGapError struct {
	err      error
	streamID string
	expected int64
}

func (e *liveGapError) Error() string { return e.err.Error() }
func (e *liveGapError) Unwrap() error { return e.err }

// Publisher broadcasts committed store events without re-inserting them.
type Publisher interface {
	PublishCommitted(events []store.Event)
}

// Projection coordinates gRPC EventStream subscription and WAL checkpointing into the Store.
type Projection struct {
	client    *daemonclient.Client
	store     *store.Store
	publisher Publisher
	projector *store.Projector

	// Per-frame execution context protected by frameMu
	frameMu sync.Mutex
	currCtx context.Context
	currEnv *sessiondv1.EventEnvelope

	// Reconnect tuning
	minBackoff time.Duration
	maxBackoff time.Duration

	mu             sync.Mutex
	running        bool
	retiredStreams map[string]bool

	registrarMu sync.RWMutex
	registrar   func(agentID, nativeID string)
}

// SetNativeSessionRegistrar sets the callback that lists a native session the
// App does not know yet. It is called from inside a projection transaction, so
// it must return immediately and do its work in the background.
func (p *Projection) SetNativeSessionRegistrar(register func(agentID, nativeID string)) {
	p.registrarMu.Lock()
	p.registrar = register
	p.registrarMu.Unlock()
}

func (p *Projection) nativeRegistrar() func(agentID, nativeID string) {
	p.registrarMu.RLock()
	defer p.registrarMu.RUnlock()
	return p.registrar
}

var (
	ErrUnknownEvent     = errors.New("projection: unknown unhandled event")
	ErrResyncRequired   = errors.New("projection: daemon requested resync")
	ErrNonContiguous    = errors.New("projection: stream sequence non-contiguous")
	ErrInvalidWatermark = errors.New("projection: invalid watermark or epoch mismatch")
	ErrCorruptedFrame   = errors.New("projection: corrupted frame received")
	ErrStorageFailure   = errors.New("projection: storage failure")
	errRetiredStream    = errors.New("projection: retired stream cursor removed from subscription")
)

// New creates a Projection manager bound to client, store, and publisher.
func New(client *daemonclient.Client, st *store.Store, pub Publisher) (*Projection, error) {
	if client == nil {
		return nil, errors.New("daemon client is required")
	}
	if st == nil {
		return nil, errors.New("store is required")
	}
	if pub == nil {
		return nil, errors.New("publisher is required")
	}

	p := &Projection{
		client:         client,
		store:          st,
		publisher:      pub,
		minBackoff:     100 * time.Millisecond,
		maxBackoff:     5 * time.Second,
		retiredStreams: make(map[string]bool),
	}

	projName := fmt.Sprintf("daemon-projection-%d", projectionCounter.Add(1))
	projector, err := st.RegisterProjector(projName, func(tx *sql.Tx) ([]store.Event, error) {
		p.frameMu.Lock()
		ctx := p.currCtx
		env := p.currEnv
		p.frameMu.Unlock()
		if env == nil || ctx == nil {
			return nil, errors.New("dynamic projection must be invoked through bound context")
		}
		events, err := p.projectEnvelopeTx(ctx, tx, env)
		if err != nil {
			return nil, err
		}
		notices, err := notifications.Events(ctx, tx, events)
		return append(events, notices...), err
	})
	if err != nil {
		return nil, fmt.Errorf("register frame projector: %w", err)
	}
	p.projector = projector

	return p, nil
}

// Run connects to the daemon EventStream, catches up on replay, advances checkpoints atomically,
// and projects live frames until context is cancelled.
func (p *Projection) Run(ctx context.Context) error {
	return p.RunReady(ctx, nil)
}

// RunReady runs the projection and closes or signals ready when live barrier is verified.
func (p *Projection) RunReady(ctx context.Context, ready chan<- struct{}) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errors.New("projection is already running")
	}
	p.running = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	var readyOnce sync.Once
	signalReady := func() {
		if ready != nil {
			readyOnce.Do(func() {
				select {
				case ready <- struct{}{}:
				default:
				}
			})
		}
	}

	backoff := p.minBackoff
	liveGapAttempts := make(map[string]int64)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := p.runSubscription(ctx, signalReady)
		var gap *liveGapError
		if errors.As(err, &gap) {
			if liveGapAttempts[gap.streamID] == gap.expected {
				return gap.err
			}
			liveGapAttempts[gap.streamID] = gap.expected
			slog.WarnContext(ctx, "live event gap; requesting durable replay", "stream_id", gap.streamID, "expected_seq", gap.expected)
			continue
		}
		if errors.Is(err, errRetiredStream) {
			// Only the confirmed unknown stream is omitted on the next read-only
			// subscription. Persisted history and all other cursors are retained.
			continue
		}
		if err == nil {
			// On clean EOF from server, reconnect read-only subscription unless ctx done
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				// Reconnect loop continues
			}
		} else if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
			return nil
		} else if isFatalProjectionError(err) {
			// Actionable/fatal errors that must not be blindly retried
			return err
		}

		// Controlled reconnect with bounded jitter
		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		sleepDuration := backoff + jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
		}

		backoff *= 2
		if backoff > p.maxBackoff {
			backoff = p.maxBackoff
		}
	}
}

// IsFatalProjectionError tests if an error represents an unrecoverable projection issue.
func IsFatalProjectionError(err error) bool {
	return isFatalProjectionError(err)
}

func isFatalProjectionError(err error) bool {
	if errors.Is(err, ErrUnknownEvent) ||
		errors.Is(err, ErrResyncRequired) ||
		errors.Is(err, ErrNonContiguous) ||
		errors.Is(err, ErrInvalidWatermark) ||
		errors.Is(err, ErrCorruptedFrame) ||
		errors.Is(err, ErrStorageFailure) {
		return true
	}
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument,
		codes.FailedPrecondition, codes.Unimplemented:
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown event") ||
		strings.Contains(msg, "resync required") ||
		strings.Contains(msg, "non-contiguous") ||
		strings.Contains(msg, "corrupted") ||
		strings.Contains(msg, "storage failure")
}

// runSubscription runs a single EventStream subscription session.
func (p *Projection) runSubscription(ctx context.Context, signalReady func()) error {
	epoch := p.client.DaemonID()
	if epoch == "" {
		return errors.New("client daemon ID is empty")
	}

	checkpoints, err := p.store.ListCheckpoints(ctx, epoch)
	if err != nil {
		return fmt.Errorf("list checkpoints: %w", err)
	}
	for streamID := range p.retiredStreams {
		delete(checkpoints, streamID)
	}

	cursors := make([]*sessiondv1.SessionCursor, 0, len(checkpoints))
	for streamID, seq := range checkpoints {
		cursors = append(cursors, &sessiondv1.SessionCursor{
			Session:    &sessiondv1.SessionRef{SessionId: streamID},
			AfterSeqId: seq,
		})
	}

	subReq := &sessiondv1.SubscribeRequest{
		ExpectedDaemonId: &epoch,
		Cursors:          cursors,
		IncludeSnapshot:  true,
		ReplayPageSize:   256,
	}

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	unregister := p.client.RegisterSubscription(cancel)
	defer unregister()

	stream, err := p.client.Events.Subscribe(subCtx, subReq)
	if err != nil {
		return fmt.Errorf("subscribe event stream: %w", err)
	}

	var liveBarrierPassed bool
	var snapshotReceived bool
	watermarks := make(map[string]int64)

	// Local cache of expected next sequences
	expectedSeq := make(map[string]int64)
	for streamID, seq := range checkpoints {
		expectedSeq[streamID] = seq + 1
	}

	for {
		frame, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
				return nil
			}
			return err
		}

		switch f := frame.GetFrame().(type) {
		case *sessiondv1.EventStreamFrame_Snapshot:
			if liveBarrierPassed {
				return fmt.Errorf("%w: snapshot received after live ready", ErrCorruptedFrame)
			}
			if snapshotReceived {
				return fmt.Errorf("%w: duplicate snapshot frame", ErrCorruptedFrame)
			}
			snapshotReceived = true
			snap := f.Snapshot
			if snap == nil {
				return fmt.Errorf("%w: nil snapshot frame", ErrCorruptedFrame)
			}
			if snap.GetDaemonId() != epoch {
				return fmt.Errorf("%w: snapshot daemon ID %s != epoch %s", ErrInvalidWatermark, snap.GetDaemonId(), epoch)
			}
			for _, sess := range snap.GetSessions() {
				sid := sess.GetSession().GetSessionId()
				if sid != "" {
					if p.retiredStreams[sid] {
						return fmt.Errorf("%w: retired stream identity was reused: %s", ErrInvalidWatermark, sid)
					}
					watermarks[sid] = sess.GetReplayThroughSeqId()
					// Check that current stored checkpoint doesn't exceed watermark
					if cp, ok := checkpoints[sid]; ok && cp > sess.GetReplayThroughSeqId() {
						return fmt.Errorf("%w: store checkpoint %d exceeds daemon watermark %d for session %s",
							ErrInvalidWatermark, cp, sess.GetReplayThroughSeqId(), sid)
					}
				}
			}

		case *sessiondv1.EventStreamFrame_Replay:
			if liveBarrierPassed {
				return fmt.Errorf("%w: replay received after live ready", ErrCorruptedFrame)
			}
			if !snapshotReceived {
				return fmt.Errorf("%w: replay arrived before snapshot", ErrCorruptedFrame)
			}
			sid := f.Replay.GetSession().GetSessionId()
			wm, exists := watermarks[sid]
			if !exists {
				return fmt.Errorf("%w: replay session %s missing in snapshot watermarks", ErrInvalidWatermark, sid)
			}
			if f.Replay.GetSeqId() > wm {
				return fmt.Errorf("%w: replay seq %d exceeds snapshot watermark %d for session %s",
					ErrInvalidWatermark, f.Replay.GetSeqId(), wm, sid)
			}
			if err := p.processEnvelope(ctx, epoch, f.Replay, expectedSeq); err != nil {
				return err
			}

		case *sessiondv1.EventStreamFrame_LiveReady:
			if !snapshotReceived {
				return fmt.Errorf("%w: live ready arrived without snapshot", ErrCorruptedFrame)
			}
			ready := f.LiveReady
			if ready == nil || ready.GetDaemonId() != epoch {
				return fmt.Errorf("%w: live ready daemon ID %v != epoch %s", ErrInvalidWatermark, ready.GetDaemonId(), epoch)
			}
			// Verify all frozen snapshot watermarks are matched exactly and all streams caught up
			readyWatermarks := make(map[string]int64, len(ready.GetWatermarks()))
			for _, wm := range ready.GetWatermarks() {
				sid := wm.GetSession().GetSessionId()
				if sid != "" {
					readyWatermarks[sid] = wm.GetReplayThroughSeqId()
				}
			}
			for sid, expectedThrough := range watermarks {
				actualThrough, ok := readyWatermarks[sid]
				if !ok || actualThrough != expectedThrough {
					return fmt.Errorf("%w: live ready watermark mismatch for session %s (expected %d, got %d)",
						ErrInvalidWatermark, sid, expectedThrough, actualThrough)
				}
				// Verify store checkpoint reached watermark
				cp, err := p.store.GetCheckpoint(ctx, epoch, sid)
				if err != nil {
					return fmt.Errorf("get checkpoint verification: %w", err)
				}
				if cp < expectedThrough {
					return fmt.Errorf("%w: stream %s checkpoint %d lagged behind watermark %d at live ready",
						ErrNonContiguous, sid, cp, expectedThrough)
				}
			}
			liveBarrierPassed = true
			if signalReady != nil {
				signalReady()
			}

		case *sessiondv1.EventStreamFrame_Live:
			if !liveBarrierPassed {
				return fmt.Errorf("%w: live event arrived before live ready barrier", ErrCorruptedFrame)
			}
			if err := p.processEnvelope(ctx, epoch, f.Live, expectedSeq); err != nil {
				if errors.Is(err, ErrNonContiguous) {
					sid := f.Live.GetSession().GetSessionId()
					return &liveGapError{err: err, streamID: sid, expected: expectedSeq[sid]}
				}
				return err
			}

		case *sessiondv1.EventStreamFrame_ResyncRequired:
			resync := f.ResyncRequired
			if resync != nil && resync.Reason == sessiondv1.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION && !snapshotReceived && !liveBarrierPassed {
				sid := resync.GetSession().GetSessionId()
				requested, exists := checkpoints[sid]
				currentEpoch := resync.GetCurrentDaemonId()
				if sid != "" && exists && !p.retiredStreams[sid] && requested == resync.RequestedAfterSeqId && resync.MinRetainedSeqId == 0 && resync.CommittedThroughSeqId == 0 && (currentEpoch == "" || currentEpoch == epoch) {
					p.retiredStreams[sid] = true
					return errRetiredStream
				}
			}
			// Fail clearly without advancing checkpoint
			return fmt.Errorf("%w: reason=%s stream=%s", ErrResyncRequired, resync.GetReason(), resync.GetSession().GetSessionId())

		case *sessiondv1.EventStreamFrame_Overflow:
			return fmt.Errorf("%w: daemon stream overflow", ErrResyncRequired)

		default:
			return fmt.Errorf("%w: unrecognized stream frame type", ErrCorruptedFrame)
		}
	}
}

func (p *Projection) processEnvelope(
	ctx context.Context,
	epoch string,
	env *sessiondv1.EventEnvelope,
	expectedSeq map[string]int64,
) error {
	if env == nil {
		return fmt.Errorf("%w: nil envelope", ErrCorruptedFrame)
	}
	if env.GetDaemonId() != epoch {
		return fmt.Errorf("%w: envelope daemon ID %s != expected epoch %s", ErrCorruptedFrame, env.GetDaemonId(), epoch)
	}
	sessionRef := env.GetSession()
	if sessionRef == nil || sessionRef.GetSessionId() == "" {
		return fmt.Errorf("%w: missing session reference in envelope", ErrCorruptedFrame)
	}
	streamID := sessionRef.GetSessionId()
	if p.retiredStreams[streamID] {
		cp, err := p.store.GetCheckpoint(ctx, epoch, streamID)
		if err != nil {
			return err
		}
		// A legacy suspended stream may return with its original durable
		// sequence. Accept only the exact next frame, never a reset or gap.
		if cp <= 0 || env.GetSeqId() != cp+1 || env.GetEventType() != "session.resumed" {
			return fmt.Errorf("%w: retired stream identity was reused: %s", ErrInvalidWatermark, streamID)
		}
		delete(p.retiredStreams, streamID)
		expectedSeq[streamID] = cp + 1
	}
	seqID := env.GetSeqId()
	if seqID <= 0 {
		return fmt.Errorf("%w: invalid non-positive seq_id %d", ErrCorruptedFrame, seqID)
	}

	exp, ok := expectedSeq[streamID]
	if !ok {
		// Look up existing durable checkpoint
		cp, err := p.store.GetCheckpoint(ctx, epoch, streamID)
		if err != nil {
			return fmt.Errorf("get checkpoint: %w", err)
		}
		exp = cp + 1
		expectedSeq[streamID] = exp
	}

	if seqID < exp {
		// Idempotent duplicate: seq already applied
		return nil
	}
	if seqID > exp {
		return fmt.Errorf("%w: stream %s expected seq %d but got %d", ErrNonContiguous, streamID, exp, seqID)
	}

	// Bind frame and context safely for single reusable projector
	p.frameMu.Lock()
	p.currCtx = ctx
	p.currEnv = env
	p.frameMu.Unlock()

	committed, err := p.store.ApplyFrame(ctx, epoch, streamID, seqID, p.projector)

	p.frameMu.Lock()
	p.currCtx = nil
	p.currEnv = nil
	p.frameMu.Unlock()

	if err != nil {
		return fmt.Errorf("apply frame: %w", err)
	}

	expectedSeq[streamID] = seqID + 1

	if len(committed) > 0 && p.publisher != nil {
		p.publisher.PublishCommitted(committed)
	}

	return nil
}

// projectEnvelopeTx parses and projects one daemon event inside the store.ApplyFrame transaction.
func (p *Projection) projectEnvelopeTx(ctx context.Context, tx *sql.Tx, env *sessiondv1.EventEnvelope) ([]store.Event, error) {
	var activityTime time.Time
	if env.GetTimestampUnixMicros() > 0 {
		activityTime = time.UnixMicro(env.GetTimestampUnixMicros()).UTC()
	}
	ctx = context.WithValue(ctx, activityTimeKey{}, activityTime)
	eventType := env.GetEventType()
	if eventType == "" {
		return nil, fmt.Errorf("%w: empty event_type", ErrCorruptedFrame)
	}

	streamID := env.GetSession().GetSessionId()
	agentID := env.GetSession().GetAgentId()

	payloadMap, err := parseNativePayload(env)
	if err != nil {
		return nil, fmt.Errorf("parse native payload: %w", err)
	}

	switch eventType {
	case "connector.hello":
		return projectConnectorHelloTx(ctx, tx, payloadMap)

	case "connector.event":
		return projectConnectorEventTx(ctx, tx, streamID, agentID, payloadMap)

	case "native.activity":
		events, unknown, err := projectNativeActivity(ctx, tx, streamID, agentID, payloadMap)
		if err == nil && unknown != nil {
			if register := p.nativeRegistrar(); register != nil {
				register(unknown.AgentID, unknown.NativeID)
			}
		}
		return events, err

	case "runtime.owner":
		return projectRuntimeOwnerTx(ctx, tx, streamID, payloadMap, env.GetRuntimeOwner())

	case "runtime.ownership_lost":
		return projectOwnershipLostTx(ctx, tx, streamID, payloadMap, env.GetOwnershipLost())

	case "codex.notification":
		return projectCodexNotificationTx(ctx, tx, streamID, agentID, payloadMap)

	case "codex.command_complete":
		return projectCodexCommandCompleteTx(ctx, tx, streamID, payloadMap)

	case "hermes.command_complete":
		return projectHermesCommandCompleteTx(ctx, tx, streamID, payloadMap, env.GetStatus())

	case "hermes.compaction":
		return projectHermesCompactionTx(ctx, tx, streamID, payloadMap)

	case "hermes.approval_request":
		return projectHermesApprovalRequestTx(ctx, tx, streamID, payloadMap)
	case "hermes.approval_resolved":
		return projectHermesApprovalResolvedTx(ctx, tx, streamID, payloadMap, env.GetStatus())

	case "hermes.stream", "hermes.notification":
		return projectHermesStreamTx(ctx, tx, streamID, payloadMap)

	case "grok.notification":
		return projectGrokNotificationTx(ctx, tx, streamID, payloadMap)

	case "grok.approval_resolved":
		return projectGrokApprovalResolvedTx(ctx, tx, streamID, payloadMap, env.GetStatus())

	case "grok.completed":
		return projectGrokCompletedTx(ctx, tx, streamID, payloadMap)

	case "token.usage", "token_metrics":
		return projectTokenMetricsTx(ctx, tx, streamID, agentID, payloadMap)

	case "task.upsert", "tasks.upsert":
		return projectTaskUpsertTx(ctx, tx, streamID, agentID, payloadMap)

	case "session.active":
		return projectSimpleSessionStatusTx(ctx, tx, streamID, agentID, "running", "session.active", payloadMap)

	case "session.idle":
		return projectSimpleSessionStatusTx(ctx, tx, streamID, agentID, "idle", "session.idle", payloadMap)

	case "session.resumed":
		return nil, nil // journal lifecycle marker; native attachment follows

	case "session.started":
		return []store.Event{{
			ID:        fmt.Sprintf("session-started-%s-%d", streamID, time.Now().UnixNano()),
			Type:      "session.started",
			SessionID: streamID,
			Data:      payloadMap,
		}}, nil

	case "session.error":
		return projectSimpleSessionStatusTx(ctx, tx, streamID, agentID, "error", "session.error", payloadMap)

	// Benign/stateless native frames that do not mutate business tables but must advance WAL checkpoint safely
	case "pty.output", "pty.closed", "terminal.output", "ping":
		return nil, nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownEvent, eventType)
	}
}

type sessionIdentity struct {
	AgentID   string
	SessionID string
}

func resolveSessionIdentity(ctx context.Context, tx *sql.Tx, streamID, explicitAgentID, explicitSessionID string) (*sessionIdentity, error) {
	sid := explicitSessionID
	if sid == "" {
		sid = streamID
	}
	aid := explicitAgentID

	if aid != "" && sid != "" {
		return &sessionIdentity{AgentID: aid, SessionID: sid}, nil
	}

	if sid != "" {
		rows, err := queryRows(ctx, tx, "SELECT agent_id, id, control_state FROM sessions WHERE id=? OR source_session_id=?", sid, sid)
		if err != nil {
			return nil, fmt.Errorf("query session identity: %w", err)
		}
		if len(rows) == 1 {
			return &sessionIdentity{
				AgentID:   fmt.Sprint(rows[0]["agent_id"]),
				SessionID: fmt.Sprint(rows[0]["id"]),
			}, nil
		}
		if len(rows) > 1 {
			var owned []map[string]any
			for _, r := range rows {
				if fmt.Sprint(r["control_state"]) == "owned" {
					owned = append(owned, r)
				}
			}
			if len(owned) == 1 {
				return &sessionIdentity{
					AgentID:   fmt.Sprint(owned[0]["agent_id"]),
					SessionID: fmt.Sprint(owned[0]["id"]),
				}, nil
			}
			if len(owned) > 1 {
				target := owned[0]
				if explicitAgentID != "" {
					for _, r := range owned {
						if fmt.Sprint(r["agent_id"]) == explicitAgentID {
							target = r
							break
						}
					}
				}
				slog.WarnContext(ctx, "ambiguous session identity resolved via fallback",
					"session_id", sid,
					"chosen_agent_id", target["agent_id"],
					"candidates", len(owned),
				)
				return &sessionIdentity{
					AgentID:   fmt.Sprint(target["agent_id"]),
					SessionID: fmt.Sprint(target["id"]),
				}, nil
			}
			target := rows[0]
			if explicitAgentID != "" {
				for _, r := range rows {
					if fmt.Sprint(r["agent_id"]) == explicitAgentID {
						target = r
						break
					}
				}
			}
			slog.WarnContext(ctx, "ambiguous session identity resolved via fallback (no owned state)",
				"session_id", sid,
				"chosen_agent_id", target["agent_id"],
				"candidates", len(rows),
			)
			return &sessionIdentity{
				AgentID:   fmt.Sprint(target["agent_id"]),
				SessionID: fmt.Sprint(target["id"]),
			}, nil
		}
	}

	if aid != "" {
		return &sessionIdentity{AgentID: aid, SessionID: sid}, nil
	}

	return nil, nil
}

func queryRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			if isJSONColumn(col) && val != nil {
				if s, ok := val.(string); ok && s != "" {
					var decoded any
					dec := json.NewDecoder(strings.NewReader(s))
					dec.UseNumber()
					if err := dec.Decode(&decoded); err == nil {
						val = decoded
					}
				}
			}
			row[col] = val
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func isJSONColumn(name string) bool {
	switch name {
	case "attachments", "capabilities", "data", "details", "logs", "progress", "runtime_owner", "tool", "value", "baseline_ids":
		return true
	default:
		return false
	}
}

func stringValue(payload map[string]any, key string, required bool) (string, error) {
	if v, ok := payload[key]; ok && v != nil {
		s := fmt.Sprint(v)
		if required && strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("%s is required", key)
		}
		return s, nil
	}
	if required {
		return "", fmt.Errorf("%s is required", key)
	}
	return "", nil
}

func payloadStringDefault(payload map[string]any, key, fallback string) string {
	if v, ok := payload[key]; ok && v != nil {
		s := fmt.Sprint(v)
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return fallback
}

func nullablePayload(payload map[string]any, key string) any {
	if v, ok := payload[key]; ok && v != nil {
		if s, ok := v.(string); ok && s == "" {
			return nil
		}
		return v
	}
	return nil
}

func preserveNilPayload(payload map[string]any, key string, fallback any) any {
	if v, ok := payload[key]; ok && v != nil {
		return v
	}
	return fallback
}

func preserveEmptyPayload(payload map[string]any, key string, fallback any) any {
	if v, ok := payload[key]; ok && v != nil {
		s := fmt.Sprint(v)
		if s != "" {
			return s
		}
	}
	return fallback
}

func isPlaceholderTitle(title, id string) bool {
	return store.IsPlaceholderSessionTitle(title, id)
}

func truncateCodepoints(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}

func findMatchingApprovalID(ctx context.Context, tx *sql.Tx, agentID, sessionID string, reqID any) (string, error) {
	rows, err := queryRows(ctx, tx, `SELECT value FROM workspace_preferences WHERE instr("key", 'approval:') = 1`)
	if err != nil {
		return "", fmt.Errorf("query approval preferences: %w", err)
	}
	target, err := json.Marshal(reqID)
	if err != nil {
		return "", err
	}
	matched := ""
	for _, row := range rows {
		val, ok := row["value"].(map[string]any)
		if !ok {
			continue
		}
		if val["agent_id"] != agentID || val["session_id"] != sessionID || val["state"] != "pending" {
			continue
		}
		if nid, ok := val["native_id"]; ok {
			encoded, err := json.Marshal(nid)
			if err != nil {
				return "", err
			}
			if bytes.Equal(encoded, target) {
				id, _ := val["id"].(string)
				if matched != "" && matched != id {
					return "", fmt.Errorf("%w: ambiguous native approval identity", ErrCorruptedFrame)
				}
				matched = id
			}
		}
	}
	return matched, nil
}

func projectSimpleSessionStatusTx(
	ctx context.Context,
	tx *sql.Tx,
	streamID, agentID, newStatus, eventType string,
	payload map[string]any,
) ([]store.Event, error) {
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	aid, _ := payload["agent_id"].(string)
	if aid == "" {
		aid = agentID
	}

	ident, err := resolveSessionIdentity(ctx, tx, streamID, aid, sid)
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, nil
	}

	err = updateSessionStatusTx(ctx, tx, ident.AgentID, ident.SessionID, newStatus)
	if err != nil {
		return nil, fmt.Errorf("update session status %s: %w", newStatus, err)
	}

	sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", ident.AgentID, ident.SessionID)
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}

	var events []store.Event
	if len(sessRows) > 0 {
		events = append(events, sessionUpsertEvent(ident.AgentID, ident.SessionID, sessRows[0]))
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("%s-%s-%d", eventType, ident.SessionID, time.Now().UnixNano()),
		Type:      eventType,
		AgentID:   ident.AgentID,
		SessionID: ident.SessionID,
		Data:      payload,
	})
	return events, nil
}

func projectHermesApprovalRequestTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	appID, _ := payload["approval_id"].(string)
	if appID == "" {
		return nil, fmt.Errorf("%w: hermes approval request missing approval_id", ErrCorruptedFrame)
	}

	var events []store.Event
	if agentID != "" && sid != "" {
		err := updateSessionStatusTx(ctx, tx, agentID, sid, "waiting_approval")
		if err != nil {
			return nil, fmt.Errorf("update hermes waiting approval: %w", err)
		}
		sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
		if err != nil {
			return nil, fmt.Errorf("query session: %w", err)
		}
		if len(sessRows) > 0 {
			events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
		}
	}

	appData := map[string]any{
		"id":         appID,
		"agent_id":   agentID,
		"session_id": sid,
		"title":      "Hermes 请求执行授权",
		"detail":     fmt.Sprint(payload["method"]),
		"state":      "pending",
		"target_id":  appID,
	}
	if nid, ok := payload["native_id"]; ok && nid != nil {
		appData["native_id"] = nid
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("approval-%s-%d", appID, time.Now().UnixNano()),
		Type:      "approval.upsert",
		AgentID:   agentID,
		SessionID: sid,
		Data:      appData,
	})
	return events, nil
}

func projectHermesApprovalResolvedTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any, envStatus sessiondv1.SessionStatus) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident == nil || ident.AgentID == "" || ident.SessionID == "" {
		return nil, fmt.Errorf("%w: hermes approval resolution missing session identity", ErrCorruptedFrame)
	}
	agentID, sid = ident.AgentID, ident.SessionID
	approvalID, _ := payload["approval_id"].(string)
	state, _ := payload["state"].(string)
	if approvalID == "" || state != "approved" && state != "rejected" && state != "resolved" {
		return nil, fmt.Errorf("%w: invalid hermes approval resolution", ErrCorruptedFrame)
	}
	data := map[string]any{"id": approvalID, "agent_id": agentID, "session_id": sid, "state": state}
	if nativeID, ok := payload["native_id"]; ok {
		data["native_id"] = nativeID
	}
	events := []store.Event{{ID: fmt.Sprintf("approval-%s-%d", approvalID, time.Now().UnixNano()), Type: "approval.upsert", AgentID: agentID, SessionID: sid, Data: data}}
	var currentStatus string
	switch envStatus {
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		currentStatus = "running"
	case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
		currentStatus = "idle"
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		currentStatus = "waiting_approval"
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		currentStatus = "error"
	default:
		return nil, fmt.Errorf("%w: hermes approval resolution missing authoritative status", ErrCorruptedFrame)
	}
	if err := updateSessionStatusTx(ctx, tx, agentID, sid, currentStatus); err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		events = append(events, sessionUpsertEvent(agentID, sid, rows[0]))
	}
	commandID, _ := payload["command_id"].(string)
	if commandID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE commands SET state='completed',error=NULL,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`, agentID, sid, commandID); err != nil {
			return nil, err
		}
		rows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agentID, sid, commandID)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			events = append(events, store.Event{ID: fmt.Sprintf("cmd-%s-%d", commandID, time.Now().UnixNano()), Type: "command.upsert", AgentID: agentID, SessionID: sid, Data: rows[0]})
		}
	}
	return events, nil
}

func projectHermesStreamTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	frame, _ := payload["frame"].(map[string]any)
	if frame == nil {
		frame = payload
	}

	if phase, _ := frame["type"].(string); strings.HasPrefix(phase, "subagent.") {
		data, _ := frame["payload"].(map[string]any)
		return projectHermesSubagentTx(ctx, tx, sid, agentID, phase, data)
	}
	if frame["type"] == "todo.updated" {
		data, _ := frame["payload"].(map[string]any)
		return projectNativePlanTx(ctx, tx, sid, agentID, "hermes", data)
	}
	if frame["type"] == "session.control.update" {
		data, _ := frame["payload"].(map[string]any)
		control, ok := data["control"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: Hermes control missing object", ErrCorruptedFrame)
		}
		raw, present := control["goal"]
		if !present {
			return nil, fmt.Errorf("%w: Hermes control missing goal", ErrCorruptedFrame)
		}
		goal, ok := raw.(map[string]any)
		if raw != nil && !ok {
			return nil, fmt.Errorf("%w: invalid Hermes goal", ErrCorruptedFrame)
		}
		return projectNativeGoalTx(ctx, tx, sid, agentID, "hermes", goal)
	}

	if frame["type"] == "session.usage" {
		data, _ := frame["payload"].(map[string]any)
		usage, _ := data["usage"].(map[string]any)
		// A lazy gateway has no context measurement; its zero totals must not
		// replace a real measurement from the running native Agent.
		if usage["context_used"] == nil {
			return nil, nil
		}
		return projectTokenMetricsTx(ctx, tx, sid, agentID, map[string]any{
			"agent_id": agentID, "session_id": sid, "model": usage["model"],
			"input_tokens": usage["input"], "output_tokens": usage["output"], "cached_tokens": usage["cache_read"],
			"reasoning_tokens": usage["reasoning"], "total_tokens": usage["total"],
			"context_window": usage["context_max"], "last_input_tokens": usage["context_used"],
		})
	}

	if phase, _ := frame["type"].(string); phase == "tool.start" || phase == "tool.complete" {
		data, _ := frame["payload"].(map[string]any)
		toolID, _ := data["tool_id"].(string)
		if toolID == "" {
			return nil, fmt.Errorf("native Hermes tool missing tool_id")
		}
		state := "running"
		text := ""
		if phase == "tool.complete" {
			state = "completed"
			text, _ = data["result_text"].(string)
			if text == "" {
				text, _ = data["summary"].(string)
			}
		}
		message := map[string]any{"id": "hermes-tool-" + toolID, "session_id": sid, "role": "tool", "kind": "tool", "text": text, "attachments": []any{}, "command_id": payload["command_id"], "tool": map[string]any{"name": data["name"], "arguments": data["args"], "status": state}}
		messageEvents, err := projectConnectorEventTx(ctx, tx, streamID, agentID, map[string]any{"type": "message.upsert", "data": message})
		if err != nil {
			return nil, err
		}
		taskEvents, err := projectTaskUpsertTx(ctx, tx, sid, agentID, map[string]any{"id": "hermes:tool:" + toolID, "kind": "tool", "title": data["name"], "status": state, "progress": map[string]any{"source": "hermes", "arguments": data["args"], "output": text}, "logs": []any{}})
		return append(messageEvents, taskEvents...), err
	}

	var events []store.Event
	delta, _ := frame["delta"].(string)
	if delta == "" {
		delta, _ = payload["delta"].(string)
	}
	msgID, _ := frame["id"].(string)
	if msgID == "" {
		msgID, _ = payload["id"].(string)
	}
	commandID, _ := payload["command_id"].(string)
	if msgID == "" && commandID != "" {
		msgID = "hermes-stream-" + commandID
	}

	if msgID != "" && delta != "" && agentID != "" && sid != "" {
		events = append(events, liveMessageEvent(agentID, sid, msgID, "assistant", "message", delta, commandID, true))
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("hermes-stream-%s-%d", sid, time.Now().UnixNano()),
		Type:      "hermes.stream",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func parseNativePayload(env *sessiondv1.EventEnvelope) (map[string]any, error) {
	if env.NativePayload != nil && len(env.NativePayload.Utf8Json) > 0 {
		dec := json.NewDecoder(bytes.NewReader(env.NativePayload.Utf8Json))
		dec.UseNumber()
		var res map[string]any
		if err := dec.Decode(&res); err != nil {
			return nil, fmt.Errorf("decode NativePayload: %w", err)
		}
		return res, nil
	}
	return make(map[string]any), nil
}

func projectConnectorHelloTx(ctx context.Context, tx *sql.Tx, payload map[string]any) ([]store.Event, error) {
	if err := store.NormalizeGrokRegistrationTx(ctx, tx, payload); err != nil {
		return nil, fmt.Errorf("normalize Grok registration: %w", err)
	}
	agentID, ok := payload["id"].(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("%w: connector.hello missing id", ErrCorruptedFrame)
	}

	kind, _ := payload["kind"].(string)
	name, _ := payload["name"].(string)
	status, _ := payload["status"].(string)
	limitation, _ := payload["limitation"].(string)
	profileName, _ := payload["profile_name"].(string)
	controlState, _ := payload["control_state"].(string)
	if controlState == "" {
		controlState = "unknown"
	}
	sourceID, _ := payload["source_id"].(string)
	if sourceID == "" {
		sourceID = agentID
	}
	runtimeID, _ := payload["runtime_id"].(string)
	if runtimeID == "" {
		runtimeID = agentID
	}
	var connectionID any = payload["connection_id"]

	capsJSON, err := json.Marshal(payload["capabilities"])
	if err != nil {
		capsJSON = []byte("[]")
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,kind,name,status,capabilities,limitation,source_id,connection_id,profile_name,runtime_id,control_state,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,status=excluded.status,capabilities=excluded.capabilities,
limitation=excluded.limitation,source_id=excluded.source_id,connection_id=excluded.connection_id,
profile_name=excluded.profile_name,runtime_id=excluded.runtime_id,control_state=excluded.control_state,updated_at=excluded.updated_at`,
		agentID, kind, name, status, string(capsJSON), limitation, sourceID, connectionID, profileName, runtimeID, controlState)
	if err != nil {
		return nil, fmt.Errorf("upsert agent tx: %w", err)
	}

	event := store.Event{
		ID:        fmt.Sprintf("agent-hello-%s-%d", agentID, time.Now().UnixNano()),
		Type:      "agent.upsert",
		AgentID:   agentID,
		SessionID: "",
		Data:      payload,
	}
	return []store.Event{event}, nil
}

func projectConnectorEventTx(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, error) {
	evAgentID, _ := payload["agent_id"].(string)
	if evAgentID == "" {
		evAgentID = agentID
	}
	evType, _ := payload["type"].(string)
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		data = payload
	}

	sid, _ := data["session_id"].(string)
	if sid == "" {
		sid, _ = payload["session_id"].(string)
	}
	if sid == "" && (evType == "session.upsert" || evType == "session.delete") {
		sid, _ = data["id"].(string)
	}
	if sid == "" {
		sid = streamID
	}

	var events []store.Event

	switch evType {
	case "session.upsert":
		if _, ok := data["agent_id"]; !ok && evAgentID != "" {
			data["agent_id"] = evAgentID
		}
		if _, ok := data["id"]; !ok && sid != "" {
			data["id"] = sid
		}
		updatedSess, err := upsertSessionTx(ctx, tx, data)
		if err != nil {
			return nil, fmt.Errorf("upsert session tx: %w", err)
		}
		if updatedSess == nil {
			return nil, nil
		}
		events = append(events, sessionUpsertEvent(evAgentID, sid, updatedSess))

	case "session.delete":
		delID, _ := data["id"].(string)
		if delID == "" {
			delID = sid
		}
		if delID != "" && evAgentID != "" {
			if _, err := store.DeleteSessionTx(ctx, tx, evAgentID, delID); err != nil {
				return nil, fmt.Errorf("delete session tx: %w", err)
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("session-del-%s-%d", delID, time.Now().UnixNano()),
				Type:      "session.delete",
				AgentID:   evAgentID,
				SessionID: delID,
				Data:      data,
			})
		}

	case "message.upsert":
		msgID, _ := data["id"].(string)
		if msgID != "" && evAgentID != "" && sid != "" {
			data["agent_id"] = evAgentID
			data["session_id"] = sid
			if data["created_at"] == nil {
				data["created_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if data["attachments"] == nil {
				data["attachments"] = []any{}
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("msg-%s-%d", msgID, time.Now().UnixNano()),
				Type:      "message.upsert",
				AgentID:   evAgentID,
				SessionID: sid,
				Data:      data,
			})
		}

	case "command.upsert":
		cmdID, _ := data["id"].(string)
		state, _ := data["state"].(string)
		if cmdID != "" && evAgentID != "" && sid != "" {
			_, err := tx.ExecContext(ctx, `UPDATE commands SET state=?,error=?,updated_at=datetime('now')
WHERE agent_id=? AND session_id=? AND id=?`, state, data["error"], evAgentID, sid, cmdID)
			if err != nil {
				return nil, fmt.Errorf("update command tx: %w", err)
			}
			cmdRows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", evAgentID, sid, cmdID)
			if err != nil || len(cmdRows) == 0 {
				return nil, fmt.Errorf("query updated command: %w", err)
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("cmd-%s-%d", cmdID, time.Now().UnixNano()),
				Type:      "command.upsert",
				AgentID:   evAgentID,
				SessionID: sid,
				Data:      cmdRows[0],
			})
		}

	case "task.upsert":
		return projectTaskUpsertTx(ctx, tx, sid, evAgentID, data)

	case "approval.upsert":
		events = append(events, store.Event{
			ID:        fmt.Sprintf("approval-%s-%d", sid, time.Now().UnixNano()),
			Type:      "approval.upsert",
			AgentID:   evAgentID,
			SessionID: sid,
			Data:      data,
		})

	default:
		return nil, fmt.Errorf("%w: connector event type %s", ErrUnknownEvent, evType)
	}

	return events, nil
}

func upsertSessionTx(ctx context.Context, tx *sql.Tx, payload map[string]any) (map[string]any, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, err
	}
	agentID, err := stringValue(payload, "agent_id", true)
	if err != nil {
		return nil, err
	}
	removed, err := store.NativeSessionRemovedTx(ctx, tx, agentID, id)
	if err != nil || removed {
		return nil, err
	}
	status, err := stringValue(payload, "status", true)
	if err != nil {
		return nil, err
	}
	title, _ := stringValue(payload, "title", false)
	title = truncateCodepoints(title, 512)
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if u, ok := payload["updated_at"].(string); ok && u != "" {
		updatedAt = u
	}
	ephemeral, _ := payload["ephemeral"].(bool)
	sourceID := payloadStringDefault(payload, "source_id", agentID)
	sourceSessionID := payloadStringDefault(payload, "source_session_id", id)
	historyState := payloadStringDefault(payload, "history_state", "local")
	controlState := payloadStringDefault(payload, "control_state", "unknown")

	existingRows, queryErr := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, id)
	if queryErr != nil {
		return nil, queryErr
	}
	if len(existingRows) == 0 {
		var runtimeOwner any
		if ro, ok := payload["runtime_owner"]; ok && ro != nil {
			b, _ := json.Marshal(ro)
			runtimeOwner = string(b)
		}
		_, execErr := tx.ExecContext(ctx, `INSERT INTO sessions
(id,agent_id,title,workspace,status,source_id,connection_id,source_session_id,project_id,project_name,
history_state,native_kind,ephemeral,control_state,runtime_owner,selected_model_provider,selected_model,
selected_reasoning_effort,selected_approval_mode,handoff_from_agent_id,handoff_from_session_id,handoff_context,
handoff_context_consumed,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, agentID, title, nullablePayload(payload, "workspace"), status, sourceID,
			nullablePayload(payload, "connection_id"), sourceSessionID, nullablePayload(payload, "project_id"),
			nullablePayload(payload, "project_name"), historyState, nullablePayload(payload, "native_kind"),
			ephemeral, controlState, runtimeOwner, nullablePayload(payload, "selected_model_provider"),
			nullablePayload(payload, "selected_model"), nullablePayload(payload, "selected_reasoning_effort"),
			nullablePayload(payload, "selected_approval_mode"), nullablePayload(payload, "handoff_from_agent_id"),
			nullablePayload(payload, "handoff_from_session_id"), nullablePayload(payload, "handoff_context"),
			false, updatedAt)
		if execErr != nil {
			return nil, execErr
		}
	} else {
		existing := existingRows[0]
		if isPlaceholderTitle(title, id) && !isPlaceholderTitle(fmt.Sprint(existing["title"]), id) {
			title = fmt.Sprint(existing["title"])
		}
		workspace := preserveNilPayload(payload, "workspace", existing["workspace"])
		projectID := preserveNilPayload(payload, "project_id", existing["project_id"])
		projectName := preserveNilPayload(payload, "project_name", existing["project_name"])
		handoffAgent := preserveEmptyPayload(payload, "handoff_from_agent_id", existing["handoff_from_agent_id"])
		handoffSession := preserveEmptyPayload(payload, "handoff_from_session_id", existing["handoff_from_session_id"])
		existingEphemeral, _ := existing["ephemeral"].(bool)
		nativeKind := existing["native_kind"]
		if _, present := payload["native_kind"]; present {
			nativeKind = payload["native_kind"]
		}
		_, execErr := tx.ExecContext(ctx, `UPDATE sessions SET title=?,workspace=?,status=?,source_id=?,connection_id=?,source_session_id=?,
project_id=?,project_name=?,history_state=?,native_kind=?,ephemeral=?,control_state=?,handoff_from_agent_id=?,handoff_from_session_id=?,updated_at=?
WHERE agent_id=? AND id=?`, title, workspace, status, sourceID, nullablePayload(payload, "connection_id"), sourceSessionID,
			projectID, projectName, historyState, nativeKind, existingEphemeral || ephemeral, controlState,
			handoffAgent, handoffSession, updatedAt, agentID, id)
		if execErr != nil {
			return nil, execErr
		}
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, id)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("query upserted session: %w", err)
	}
	return rows[0], nil
}

func projectRuntimeOwnerTx(
	ctx context.Context,
	tx *sql.Tx,
	streamID string,
	payload map[string]any,
	pbOwner *sessiondv1.RuntimeOwnerEvent,
) ([]store.Event, error) {
	var pid int64
	if pbOwner != nil && pbOwner.OwnerPid != nil {
		pid = *pbOwner.OwnerPid
	} else if pval, ok := payload["owner_pid"]; ok {
		switch num := pval.(type) {
		case json.Number:
			pid, _ = num.Int64()
		case int64:
			pid = num
		case float64:
			pid = int64(num)
		}
	}

	if pid <= 0 {
		// Early agy builds emitted terminal status under runtime.owner without
		// a PID. Replay that exact legacy shape as status, never as ownership.
		agentID, _ := payload["agent_id"].(string)
		status, _ := payload["status"].(string)
		if payload["owner_pid"] == nil && payload["runtime_owned"] == true && agentID != "" && (status == "idle" || status == "error") {
			var kind string
			if err := tx.QueryRowContext(ctx, "SELECT kind FROM agents WHERE id=?", agentID).Scan(&kind); err != nil {
				return nil, err
			}
			if kind == "antigravity" {
				return projectSimpleSessionStatusTx(ctx, tx, streamID, agentID, status, "session.status", payload)
			}
		}
		return nil, fmt.Errorf("%w: invalid runtime owner pid", ErrCorruptedFrame)
	}

	explicitAgent, _ := payload["agent_id"].(string)
	explicitSession, _ := payload["session_id"].(string)
	ident, err := resolveSessionIdentity(ctx, tx, streamID, explicitAgent, explicitSession)
	if err != nil {
		return nil, err
	}
	if ident == nil {
		// Valid PTY / runtime owner without App session: acknowledged clean no-op
		return nil, nil
	}

	ownerJSON, _ := json.Marshal(map[string]any{"owner_pid": pid, "session_id": ident.SessionID})
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET runtime_owner=? WHERE agent_id=? AND id=?`,
		string(ownerJSON), ident.AgentID, ident.SessionID)
	if err != nil {
		return nil, fmt.Errorf("update session runtime_owner: %w", err)
	}

	events := []store.Event{{
		ID:        fmt.Sprintf("runtime-owner-%s-%s-%d", ident.AgentID, ident.SessionID, time.Now().UnixNano()),
		Type:      "runtime.owner",
		AgentID:   ident.AgentID,
		SessionID: ident.SessionID,
		Data:      payload,
	}}
	// Publish the owner change without treating process registration as activity.
	sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", ident.AgentID, ident.SessionID)
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	if len(sessRows) > 0 {
		events = append(events, sessionUpsertEvent(ident.AgentID, ident.SessionID, sessRows[0]))
	}
	return events, nil
}

func projectOwnershipLostTx(
	ctx context.Context,
	tx *sql.Tx,
	streamID string,
	payload map[string]any,
	pbLost *sessiondv1.OwnershipLostEvent,
) ([]store.Event, error) {
	orphanAlive := false
	if pbLost != nil {
		orphanAlive = pbLost.GetOrphanAlive()
	} else if o, ok := payload["orphan_alive"].(bool); ok {
		orphanAlive = o
	}

	reason := "小内核已重启，上一条指令的执行结果无法确认；不会自动重发。"
	runtimeOwnerJSON, _ := json.Marshal(map[string]any{
		"orphan_alive": orphanAlive,
		"adopted":      false,
	})

	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	aid, _ := payload["agent_id"].(string)

	var rows []map[string]any
	var err error
	if aid != "" {
		rows, err = queryRows(ctx, tx, "SELECT agent_id, id FROM sessions WHERE agent_id=? AND (id=? OR source_session_id=?) AND control_state='owned'", aid, sid, sid)
	} else {
		rows, err = queryRows(ctx, tx, "SELECT agent_id, id FROM sessions WHERE (id=? OR source_session_id=?) AND control_state='owned'", sid, sid)
	}
	if err != nil {
		return nil, fmt.Errorf("query owned sessions: %w", err)
	}

	var events []store.Event
	for _, row := range rows {
		agent := fmt.Sprint(row["agent_id"])
		sess := fmt.Sprint(row["id"])

		_, err = tx.ExecContext(ctx, `UPDATE sessions SET status='error',runtime_owner=? WHERE agent_id=? AND id=?`,
			string(runtimeOwnerJSON), agent, sess)
		if err != nil {
			return nil, fmt.Errorf("update session ownership lost: %w", err)
		}

		commands, err := queryRows(ctx, tx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? AND state IN ('received','queued','accepted','running')`, agent, sess)
		if err != nil {
			return nil, fmt.Errorf("query commands ownership lost: %w", err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE commands SET state='unknown',error=?,updated_at=datetime('now')
WHERE agent_id=? AND session_id=? AND state IN ('received','queued','accepted','running')`, reason, agent, sess)
		if err != nil {
			return nil, fmt.Errorf("update commands ownership lost: %w", err)
		}
		for _, command := range commands {
			updated, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agent, sess, command["id"])
			if err != nil {
				return nil, err
			}
			if len(updated) > 0 {
				events = append(events, store.Event{ID: fmt.Sprintf("cmd-%s-%d", command["id"], time.Now().UnixNano()), Type: "command.upsert", AgentID: agent, SessionID: sess, Data: updated[0]})
			}
		}

		events = append(events, store.Event{
			ID:        fmt.Sprintf("ownership-lost-%s-%s-%d", agent, sess, time.Now().UnixNano()),
			Type:      "runtime.ownership_lost",
			AgentID:   agent,
			SessionID: sess,
			Data:      payload,
		})
		if updated, qErr := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agent, sess); qErr == nil && len(updated) > 0 {
			events = append(events, sessionUpsertEvent(agent, sess, updated[0]))
		}
	}

	return events, nil
}

func projectCodexNotificationTx(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, error) {
	frame, _ := payload["frame"].(map[string]any)
	if frame == nil {
		frame = payload
	}

	params, _ := frame["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}

	explicitAgent := agentID
	if explicitAgent == "" {
		explicitAgent, _ = payload["agent_id"].(string)
	}
	explicitSession, _ := params["threadId"].(string)
	if explicitSession == "" {
		explicitSession, _ = payload["session_id"].(string)
	}

	ident, err := resolveSessionIdentity(ctx, tx, streamID, explicitAgent, explicitSession)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		explicitAgent = ident.AgentID
		explicitSession = ident.SessionID
	}

	method, _ := frame["method"].(string)
	var events []store.Event

	switch method {
	case "turn/plan/updated":
		return projectNativePlanTx(ctx, tx, explicitSession, explicitAgent, "codex", params)
	case "thread/goal/updated":
		goal, ok := params["goal"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: codex goal missing object", ErrCorruptedFrame)
		}
		return projectNativeGoalTx(ctx, tx, explicitSession, explicitAgent, "codex", goal)
	case "thread/goal/cleared":
		return projectNativeGoalTx(ctx, tx, explicitSession, explicitAgent, "codex", nil)
	case "thread/tokenUsage/updated":
		usage, _ := params["tokenUsage"].(map[string]any)
		last, _ := usage["last"].(map[string]any)
		total, _ := usage["total"].(map[string]any)
		return projectTokenMetricsTx(ctx, tx, explicitSession, explicitAgent, map[string]any{
			"agent_id": explicitAgent, "session_id": explicitSession, "model": "native",
			"input_tokens": total["inputTokens"], "output_tokens": total["outputTokens"],
			"cached_tokens": total["cachedInputTokens"], "reasoning_tokens": total["reasoningOutputTokens"],
			"total_tokens": total["totalTokens"], "last_input_tokens": last["inputTokens"], "context_window": usage["modelContextWindow"],
		})
	case "turn/started":
		if explicitAgent != "" && explicitSession != "" {
			err := updateSessionStatusTx(ctx, tx, explicitAgent, explicitSession, "running")
			if err != nil {
				return nil, fmt.Errorf("update codex session running: %w", err)
			}
			turnID, _ := params["turnId"].(string)
			if turnID == "" {
				if turnMap, ok := params["turn"].(map[string]any); ok {
					turnID, _ = turnMap["id"].(string)
				}
			}
			if turnID != "" {
				encodedKey, _ := json.Marshal([]string{explicitAgent, explicitSession})
				prefKey := "native_turn:" + string(encodedKey)
				encTurn, _ := json.Marshal(turnID)
				if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, prefKey, encTurn); err != nil {
					return nil, fmt.Errorf("persist native turn: %w", err)
				}
			}
			cmdID, _ := payload["command_id"].(string)
			if cmdID != "" {
				result, err := tx.ExecContext(ctx, `UPDATE commands SET state='running',error=NULL,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`, explicitAgent, explicitSession, cmdID)
				if err != nil {
					return nil, fmt.Errorf("update codex command running: %w", err)
				}
				affected, err := result.RowsAffected()
				if err != nil {
					return nil, err
				}
				if affected > 0 && turnID != "" {
					binding, err := json.Marshal([]string{turnID, cmdID})
					if err != nil {
						return nil, err
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, store.NativeTurnCommandKey(explicitAgent, explicitSession), string(binding)); err != nil {
						return nil, err
					}
				}
				cmdRows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", explicitAgent, explicitSession, cmdID)
				if err != nil {
					return nil, fmt.Errorf("query running command: %w", err)
				}
				if len(cmdRows) > 0 {
					events = append(events, store.Event{ID: fmt.Sprintf("cmd-%s-%d", cmdID, time.Now().UnixNano()), Type: "command.upsert", AgentID: explicitAgent, SessionID: explicitSession, Data: cmdRows[0]})
				}
			}
			sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", explicitAgent, explicitSession)
			if err != nil {
				return nil, fmt.Errorf("query session: %w", err)
			}
			if len(sessRows) > 0 {
				events = append(events, sessionUpsertEvent(explicitAgent, explicitSession, sessRows[0]))
			}
		}

	case "turn/completed":
		if explicitAgent != "" && explicitSession != "" {
			turn, _ := params["turn"].(map[string]any)
			turnStatus, _ := turn["status"].(string)
			newSessStatus := "idle"
			cmdState := "completed"
			if turnStatus == "failed" {
				newSessStatus = "error"
				cmdState = "failed"
			} else if turnStatus == "interrupted" {
				cmdState = "cancelled"
			}

			turnID, _ := turn["id"].(string)
			if turnID == "" {
				turnID, _ = params["turnId"].(string)
			}
			currentTurn := true
			if turnID != "" {
				encodedKey, _ := json.Marshal([]string{explicitAgent, explicitSession})
				prefKey := "native_turn:" + string(encodedKey)
				var storedVal string
				scanErr := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, prefKey).Scan(&storedVal)
				if scanErr == nil {
					var storedTurn string
					if err := json.Unmarshal([]byte(storedVal), &storedTurn); err == nil {
						if storedTurn == turnID {
							if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, prefKey); err != nil {
								return nil, fmt.Errorf("clear native turn: %w", err)
							}
						} else if storedTurn != "" {
							currentTurn = false
						}
					}
				} else if scanErr != sql.ErrNoRows {
					return nil, scanErr
				}
			}

			if currentTurn {
				err := updateSessionStatusTx(ctx, tx, explicitAgent, explicitSession, newSessStatus)
				if err != nil {
					return nil, fmt.Errorf("update codex session completed: %w", err)
				}
			}
			cmdID, _ := payload["command_id"].(string)
			if turnID != "" {
				bound, err := codexTurnCommandTx(ctx, tx, explicitAgent, explicitSession, turnID)
				if err != nil {
					return nil, err
				}
				if bound != "" {
					if cmdID == "" {
						cmdID = bound
					}
					if cmdID == bound {
						if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, store.NativeTurnCommandKey(explicitAgent, explicitSession)); err != nil {
							return nil, err
						}
					}
				}
			}
			if cmdID != "" {
				_, err := tx.ExecContext(ctx, `UPDATE commands SET state=?,error=NULL,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`, cmdState, explicitAgent, explicitSession, cmdID)
				if err != nil {
					return nil, fmt.Errorf("update codex command completed: %w", err)
				}
				cmdRows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", explicitAgent, explicitSession, cmdID)
				if err != nil {
					return nil, fmt.Errorf("query completed command: %w", err)
				}
				if len(cmdRows) > 0 {
					events = append(events, store.Event{ID: fmt.Sprintf("cmd-%s-%d", cmdID, time.Now().UnixNano()), Type: "command.upsert", AgentID: explicitAgent, SessionID: explicitSession, Data: cmdRows[0]})
				}
			}
			if currentTurn {
				sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", explicitAgent, explicitSession)
				if err != nil {
					return nil, fmt.Errorf("query session: %w", err)
				}
				if len(sessRows) > 0 {
					events = append(events, sessionUpsertEvent(explicitAgent, explicitSession, sessRows[0]))
				}
			}
		}

	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput":
		if explicitAgent != "" && explicitSession != "" {
			err := updateSessionStatusTx(ctx, tx, explicitAgent, explicitSession, "waiting_approval")
			if err != nil {
				return nil, fmt.Errorf("update codex waiting approval: %w", err)
			}
			appID, _ := payload["approval_id"].(string)
			if appID == "" {
				if rawID, ok := frame["id"]; ok && rawID != nil {
					appID = "codex:" + fmt.Sprint(rawID)
				}
			}
			if appID == "" {
				return nil, fmt.Errorf("%w: codex approval request missing approval_id", ErrCorruptedFrame)
			}
			appData := map[string]any{
				"id":         appID,
				"agent_id":   explicitAgent,
				"session_id": explicitSession,
				"title":      "Codex 请求执行授权",
				"method":     method,
				"detail":     fmt.Sprint(params["command"]),
				"state":      "pending",
				"target_id":  appID,
			}
			if rawID, ok := frame["id"]; ok && rawID != nil {
				appData["native_id"] = rawID
			}
			if method == "item/tool/requestUserInput" {
				questions, ok := params["questions"].([]any)
				if !ok || len(questions) == 0 {
					return nil, fmt.Errorf("%w: missing user input questions", ErrCorruptedFrame)
				}
				appData["title"] = "Codex 等待你的回答"
				appData["detail"] = "请选择选项或填写回答。"
				appData["questions"] = questions
				appData["item_id"] = params["itemId"]
				appData["turn_id"] = params["turnId"]
				appData["is_blocking"] = params["isBlocking"]
			}
			if method == "item/permissions/requestApproval" {
				permissions, ok := params["permissions"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%w: invalid native permission request", ErrCorruptedFrame)
				}
				appData["permissions"] = permissions
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("approval-%s-%d", appID, time.Now().UnixNano()),
				Type:      "approval.upsert",
				AgentID:   explicitAgent,
				SessionID: explicitSession,
				Data:      appData,
			})
			sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", explicitAgent, explicitSession)
			if err != nil {
				return nil, fmt.Errorf("query session: %w", err)
			}
			if len(sessRows) > 0 {
				events = append(events, sessionUpsertEvent(explicitAgent, explicitSession, sessRows[0]))
			}
		}

	case "serverRequest/resolved":
		if explicitAgent != "" && explicitSession != "" {
			decision, _ := params["decision"].(string)
			appState := "approved"
			if decision == "reject" || decision == "rejected" {
				appState = "rejected"
			}
			appID, _ := payload["approval_id"].(string)
			reqID := params["requestId"]
			if appID == "" && reqID != nil {
				// Search stored pending approvals for exact native_id matching reqID
				appID, err = findMatchingApprovalID(ctx, tx, explicitAgent, explicitSession, reqID)
				if err != nil {
					return nil, err
				}
			}
			if appID == "" {
				return nil, fmt.Errorf("%w: serverRequest/resolved cannot resolve approval ID for request %v", ErrCorruptedFrame, reqID)
			}
			appData := map[string]any{
				"id":         appID,
				"agent_id":   explicitAgent,
				"session_id": explicitSession,
				"state":      appState,
			}
			if reqID != nil {
				appData["native_id"] = reqID
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("approval-%s-%d", appID, time.Now().UnixNano()),
				Type:      "approval.upsert",
				AgentID:   explicitAgent,
				SessionID: explicitSession,
				Data:      appData,
			})
			cmdID, _ := payload["command_id"].(string)
			if cmdID != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE commands SET state='completed',error=NULL,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND id=?`, explicitAgent, explicitSession, cmdID); err != nil {
					return nil, fmt.Errorf("update resolved command: %w", err)
				}
				cmdRows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", explicitAgent, explicitSession, cmdID)
				if err != nil {
					return nil, fmt.Errorf("query resolved command: %w", err)
				}
				if len(cmdRows) > 0 {
					events = append(events, store.Event{ID: fmt.Sprintf("cmd-%s-%d", cmdID, time.Now().UnixNano()), Type: "command.upsert", AgentID: explicitAgent, SessionID: explicitSession, Data: cmdRows[0]})
				}
			}
		}

	case "item/started", "item/completed":
		if item, ok := params["item"].(map[string]any); ok && explicitAgent != "" && explicitSession != "" {
			itemID, _ := item["id"].(string)
			itemType, _ := item["type"].(string)
			if images := CodexGeneratedImages(item, explicitSession, itemID); itemID != "" && len(images) > 0 {
				event := liveMessageEvent(explicitAgent, explicitSession, itemID, "assistant", "message", "", nil, false)
				event.Data["attachments"] = images
				event.Data["turn_id"] = params["turnId"]
				return []store.Event{event}, nil
			}
			if itemType == "collabAgentToolCall" || itemType == "subAgentActivity" {
				return projectCodexSubagentsTx(ctx, tx, explicitSession, explicitAgent, item)
			}
			if itemID != "" {
				if itemType == "agentMessage" || itemType == "userMessage" || itemType == "reasoning" || itemType == "plan" {
					text, _ := item["text"].(string)
					if itemType == "userMessage" && text == "" {
						text = codexUserContentText(item["content"])
					}
					role := "assistant"
					kind := "message"
					if itemType == "reasoning" {
						kind = "thinking"
					}
					if itemType == "userMessage" {
						role = "user"
					}
					var activeCmdID sql.NullString
					commandID, _ := payload["command_id"].(string)
					if commandID == "" {
						_ = tx.QueryRowContext(ctx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? AND state='running' ORDER BY created_at DESC LIMIT 1`, explicitAgent, explicitSession).Scan(&activeCmdID)
						commandID = activeCmdID.String
					}
					var cmdIDParam any
					if commandID != "" {
						cmdIDParam = commandID
					}
					event := liveMessageEvent(explicitAgent, explicitSession, itemID, role, kind, text, cmdIDParam, false)
					event.Data["turn_id"] = params["turnId"]
					if itemType == "agentMessage" {
						event.Data["delivery"] = item["delivery"]
						event.Data["questions"] = item["questions"]
					}
					if itemType == "userMessage" {
						event.Data["attachments"] = CodexUserAttachments(item["content"], explicitSession, itemID)
					}
					events = append(events, event)
					if method == "item/completed" && itemType == "userMessage" {
						clientID, _ := item["clientId"].(string)
						turnID, _ := params["turnId"].(string)
						if clientID != "" && turnID != "" {
							original, err := codexTurnCommandTx(ctx, tx, explicitAgent, explicitSession, turnID)
							if err != nil {
								return nil, err
							}
							if original != "" && original != clientID {
								delivered, err := projectCodexCommandCompleteTx(ctx, tx, explicitSession, map[string]any{"agent_id": explicitAgent, "session_id": explicitSession, "command_id": clientID})
								if err != nil {
									return nil, err
								}
								events = append(events, delivered...)
							}
						}
					}
				} else {
					taskEvents, err := projectCodexToolTx(ctx, tx, explicitSession, explicitAgent, method, item)
					if err != nil {
						return nil, err
					}
					events = append(events, taskEvents...)
				}
			}
		}

	case "item/agentMessage/delta", "item/reasoning/delta", "item/plan/delta":
		itemID, _ := params["itemId"].(string)
		if itemID == "" {
			itemID, _ = params["id"].(string)
		}
		delta, _ := params["delta"].(string)
		if delta == "" {
			delta, _ = params["text"].(string)
		}
		if itemID != "" && explicitAgent != "" && explicitSession != "" {
			var activeCmdID sql.NullString
			_ = tx.QueryRowContext(ctx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? AND state='running' ORDER BY created_at DESC LIMIT 1`, explicitAgent, explicitSession).Scan(&activeCmdID)
			var cmdIDParam any
			if activeCmdID.Valid && activeCmdID.String != "" {
				cmdIDParam = activeCmdID.String
			}
			kind := "message"
			if method == "item/reasoning/delta" {
				kind = "thinking"
			}
			event := liveMessageEvent(explicitAgent, explicitSession, itemID, "assistant", kind, delta, cmdIDParam, true)
			event.Data["turn_id"] = params["turnId"]
			events = append(events, event)
		}
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("codex-notif-%s-%s-%d", explicitAgent, explicitSession, time.Now().UnixNano()),
		Type:      "codex.notification",
		AgentID:   explicitAgent,
		SessionID: explicitSession,
		Data:      payload,
	})
	return events, nil
}

func projectHermesCommandCompleteTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any, envStatus sessiondv1.SessionStatus) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	var events []store.Event
	commandID, _ := payload["command_id"].(string)
	if commandID != "" && agentID != "" && sid != "" {
		messageEvents, err := projectHermesFinalMessageTx(ctx, tx, agentID, sid, payload)
		if err != nil {
			return nil, err
		}
		events = append(events, messageEvents...)
		res, execErr := tx.ExecContext(ctx, `UPDATE commands SET state='completed',error=NULL,updated_at=datetime('now')
WHERE agent_id=? AND session_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`,
			agentID, sid, commandID)
		if execErr != nil {
			return nil, fmt.Errorf("update hermes command complete: %w", execErr)
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			cmdRows, _ := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agentID, sid, commandID)
			if len(cmdRows) > 0 {
				events = append(events, store.Event{
					ID:        fmt.Sprintf("cmd-%s-%d", commandID, time.Now().UnixNano()),
					Type:      "command.upsert",
					AgentID:   agentID,
					SessionID: sid,
					Data:      cmdRows[0],
				})
			}
		}
	}

	if agentID != "" && sid != "" {
		sessRows, qErr := queryRows(ctx, tx, "SELECT status FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
		if qErr != nil {
			return nil, fmt.Errorf("query session status: %w", qErr)
		}
		if len(sessRows) > 0 {
			curStatus := fmt.Sprint(sessRows[0]["status"])
			targetStatus := "idle"
			if envStatus == sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL || curStatus == "waiting_approval" {
				targetStatus = "waiting_approval"
			} else if envStatus == sessiondv1.SessionStatus_SESSION_STATUS_RUNNING {
				targetStatus = "running"
			} else if envStatus == sessiondv1.SessionStatus_SESSION_STATUS_ERROR {
				targetStatus = "error"
			}

			execErr := updateSessionStatusTx(ctx, tx, agentID, sid, targetStatus)
			if execErr != nil {
				return nil, fmt.Errorf("update hermes session status: %w", execErr)
			}
			updatedSess, _ := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
			if len(updatedSess) > 0 {
				events = append(events, sessionUpsertEvent(agentID, sid, updatedSess[0]))
			}
		}
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("hermes-cmd-%s-%d", sid, time.Now().UnixNano()),
		Type:      "hermes.command_complete",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func projectHermesCompactionTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	compactionID, _ := payload["id"].(string)
	state, _ := payload["state"].(string)

	labels := map[string]string{
		"running":   "正在压缩上下文",
		"completed": "上下文压缩完成",
		"failed":    "上下文压缩失败",
		"unknown":   "上下文压缩状态未确认",
	}
	text := labels[state]
	if text == "" {
		text = "上下文压缩"
	}

	createdAt, _ := payload["created_at"].(string)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	var events []store.Event
	if compactionID != "" && agentID != "" && sid != "" {
		event := liveMessageEvent(agentID, sid, compactionID, "system", "message", text, nil, false)
		event.Data["created_at"] = createdAt
		events = append(events, event)
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("hermes-compaction-%s-%d", sid, time.Now().UnixNano()),
		Type:      "hermes.compaction",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func extractGrokText(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
		if content, exists := v["content"]; exists {
			return extractGrokText(content)
		}
		return ""
	case []any:
		var sb strings.Builder
		for _, item := range v {
			sb.WriteString(extractGrokText(item))
		}
		return sb.String()
	default:
		return fmt.Sprint(v)
	}
}

func projectGrokNotificationTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	return handleGrokNotificationTx(ctx, tx, streamID, payload)
}

func legacyGrokNotificationTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	frame, _ := payload["frame"].(map[string]any)
	if frame == nil {
		frame = payload
	}

	var events []store.Event
	params, _ := frame["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if update != nil && agentID != "" && sid != "" {
		kind, _ := update["sessionUpdate"].(string)
		if kind == "turn_completed" {
			err := updateSessionStatusTx(ctx, tx, agentID, sid, "idle")
			if err != nil {
				return nil, fmt.Errorf("update grok session idle: %w", err)
			}
			_, err = tx.ExecContext(ctx, `UPDATE tasks SET status='completed',updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND status='running'`, agentID, sid)
			if err != nil {
				return nil, fmt.Errorf("update grok tasks completed: %w", err)
			}
			sessRows, _ := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
			if len(sessRows) > 0 {
				events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
			}
		} else if kind == "agent_message_chunk" || kind == "agent_thought_chunk" || kind == "user_message_chunk" {
			text := extractGrokText(update["content"])
			if text != "" {
				role := "assistant"
				itemType := "message"
				if kind == "agent_thought_chunk" {
					itemType = "thinking"
				} else if kind == "user_message_chunk" {
					role = "user"
				}

				token := ""
				if meta, ok := params["_meta"].(map[string]any); ok {
					token, _ = meta["promptId"].(string)
				}
				if token == "" {
					if meta, ok := update["_meta"].(map[string]any); ok {
						token, _ = meta["promptId"].(string)
					}
				}
				if token == "" {
					if meta, ok := frame["_meta"].(map[string]any); ok {
						token, _ = meta["promptId"].(string)
					}
				}

				var activeCmdID sql.NullString
				_ = tx.QueryRowContext(ctx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? AND state='running' ORDER BY created_at DESC LIMIT 1`, agentID, sid).Scan(&activeCmdID)
				if token == "" && activeCmdID.Valid {
					token = activeCmdID.String
				}
				if token == "" {
					_ = tx.QueryRowContext(ctx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? ORDER BY created_at DESC LIMIT 1`, agentID, sid).Scan(&activeCmdID)
					if activeCmdID.Valid {
						token = activeCmdID.String
					}
				}
				if token == "" {
					token = fmt.Sprintf("t-%d", time.Now().UnixMilli())
				}

				suffix := "reply"
				if kind == "agent_thought_chunk" {
					suffix = "thought"
				} else if kind == "user_message_chunk" {
					suffix = "user"
				}
				msgID := fmt.Sprintf("grok_%s_%s", token, suffix)

				var cmdIDParam any
				if activeCmdID.Valid && activeCmdID.String != "" {
					cmdIDParam = activeCmdID.String
				}

				events = append(events, liveMessageEvent(agentID, sid, msgID, role, itemType, text, cmdIDParam, true))
			}
		}
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("grok-notif-%s-%d", sid, time.Now().UnixNano()),
		Type:      "grok.notification",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func projectGrokCompletedTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	commandID, _ := payload["command_id"].(string)
	errVal := payload["error"]

	state := "completed"
	if errVal != nil {
		state = "failed"
	} else if c, ok := payload["cancelled"].(bool); ok && c {
		state = "cancelled"
	}

	var events []store.Event
	if result, ok := payload["result"].(map[string]any); ok {
		if meta, ok := result["_meta"].(map[string]any); ok {
			if usage, ok := meta["usage"].(map[string]any); ok {
				model := payload["model"]
				if models, ok := usage["modelUsage"].(map[string]any); ok && len(models) == 1 {
					for name := range models {
						model = name
					}
				}
				events = append(events, projectGrokUsageMetricsTx(ctx, tx, sid, agentID, usage, model)...)
			}
		}
	}
	cleanup, err := cleanupPendingApprovalsTx(ctx, tx, agentID, sid, "rejected")
	if err != nil {
		return nil, err
	}
	events = append(events, cleanup...)
	if commandID != "" && agentID != "" && sid != "" {
		_, err := tx.ExecContext(ctx, `UPDATE commands SET state=?,error=?,updated_at=datetime('now')
WHERE agent_id=? AND session_id=? AND id=?`, state, errVal, agentID, sid, commandID)
		if err != nil {
			return nil, fmt.Errorf("update grok command: %w", err)
		}
		cmdRows, _ := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agentID, sid, commandID)
		if len(cmdRows) > 0 {
			events = append(events, store.Event{
				ID:        fmt.Sprintf("cmd-%s-%d", commandID, time.Now().UnixNano()),
				Type:      "command.upsert",
				AgentID:   agentID,
				SessionID: sid,
				Data:      cmdRows[0],
			})
		}
	}

	sessStatus := "idle"
	if state == "failed" {
		sessStatus = "error"
	}
	if agentID != "" && sid != "" {
		err := updateSessionStatusTx(ctx, tx, agentID, sid, sessStatus)
		if err != nil {
			return nil, fmt.Errorf("update grok session: %w", err)
		}
		sessRows, _ := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
		if len(sessRows) > 0 {
			events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
		}
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("grok-comp-%s-%d", sid, time.Now().UnixNano()),
		Type:      "grok.completed",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func projectTokenMetricsTx(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, error) {
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	aid, _ := payload["agent_id"].(string)
	if aid == "" {
		aid = agentID
	}

	ident, err := resolveSessionIdentity(ctx, tx, streamID, aid, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		aid = ident.AgentID
		sid = ident.SessionID
	}

	model, _ := payload["model"].(string)
	date, _ := payload["date"].(string)
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	getInt := func(k string) int64 {
		val := payload[k]
		if num, ok := val.(json.Number); ok {
			n, _ := num.Int64()
			return n
		}
		if n, ok := val.(int64); ok {
			return n
		}
		if f, ok := val.(float64); ok {
			return int64(f)
		}
		return 0
	}

	inputTokens := getInt("input_tokens")
	outputTokens := getInt("output_tokens")
	cachedTokens := getInt("cached_tokens")
	reasoningTokens := getInt("reasoning_tokens")
	totalTokens := getInt("total_tokens")
	contextWindow := getInt("context_window")
	lastInputTokens := getInt("last_input_tokens")

	if model != "" && aid != "" && sid != "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO token_metrics
(agent_id,session_id,model,provider,date,input_tokens,output_tokens,cached_tokens,reasoning_tokens,total_tokens,context_window,last_input_tokens,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
ON CONFLICT(agent_id,session_id,model,date) DO UPDATE SET
input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,cached_tokens=excluded.cached_tokens,
reasoning_tokens=excluded.reasoning_tokens,total_tokens=excluded.total_tokens,context_window=excluded.context_window,
last_input_tokens=excluded.last_input_tokens,updated_at=excluded.updated_at`,
			aid, sid, model, payload["provider"], date, inputTokens, outputTokens, cachedTokens, reasoningTokens, totalTokens, contextWindow, lastInputTokens)
		if err != nil {
			return nil, fmt.Errorf("upsert token metrics: %w", err)
		}
	}

	return []store.Event{{
		ID:        fmt.Sprintf("token-usage-%s-%d", sid, time.Now().UnixNano()),
		Type:      "token.usage",
		AgentID:   aid,
		SessionID: sid,
		Data:      payload,
	}}, nil
}

func projectTaskUpsertTx(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, error) {
	taskID, _ := payload["id"].(string)
	if taskID == "" {
		return nil, fmt.Errorf("%w: task missing id", ErrCorruptedFrame)
	}

	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	aid, _ := payload["agent_id"].(string)
	if aid == "" {
		aid = agentID
	}

	ident, err := resolveSessionIdentity(ctx, tx, streamID, aid, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		aid = ident.AgentID
		sid = ident.SessionID
	}

	kind, _ := payload["kind"].(string)
	title, _ := payload["title"].(string)
	status, _ := payload["status"].(string)

	persisted := payload
	if kind == "tool" || kind == "subagent" {
		// Keep tool status metadata; arguments and output belong to native history.
		persisted = make(map[string]any, len(payload))
		for key, value := range payload {
			persisted[key] = value
		}
		progress, _ := payload["progress"].(map[string]any)
		metadata := map[string]any{}
		for _, key := range []string{"source", "native_status", "exit_code", "blocking", "child_session_id", "subagent_id", "delegation_id", "task_index", "tool", "model", "reasoning_effort", "agent_path", "tool_count"} {
			if value, ok := progress[key]; ok {
				metadata[key] = value
			}
		}
		persisted["progress"], persisted["command"], persisted["logs"] = metadata, nil, []any{}
	}
	progressJSON, _ := json.Marshal(persisted["progress"])
	logsJSON, _ := json.Marshal(persisted["logs"])
	if len(logsJSON) == 0 {
		logsJSON = []byte("[]")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks(id,session_id,agent_id,kind,title,status,progress,command,logs,target_id,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(agent_id,session_id,id) DO UPDATE SET kind=excluded.kind,title=excluded.title,status=excluded.status,
progress=excluded.progress,command=excluded.command,logs=excluded.logs,target_id=excluded.target_id,updated_at=excluded.updated_at`,
		taskID, sid, aid, kind, title, status, string(progressJSON), persisted["command"], string(logsJSON), payload["target_id"], now, now)
	if err != nil {
		return nil, fmt.Errorf("upsert task: %w", err)
	}

	taskRows, _ := queryRows(ctx, tx, "SELECT * FROM tasks WHERE agent_id=? AND session_id=? AND id=?", aid, sid, taskID)
	data := payload
	if len(taskRows) > 0 {
		data = taskRows[0]
	}

	events := []store.Event{{
		ID:        fmt.Sprintf("task-%s-%d", taskID, time.Now().UnixNano()),
		Type:      "task.upsert",
		AgentID:   aid,
		SessionID: sid,
		Data:      data,
	}}
	if kind == "tool" || kind == "subagent" {
		live := make(map[string]any, len(data))
		for key, value := range data {
			live[key] = value
		}
		live["progress"], live["command"], live["logs"] = payload["progress"], payload["command"], payload["logs"]
		events = append(events, store.Event{ID: fmt.Sprintf("tool-live-%s-%d", taskID, time.Now().UnixNano()), Type: "task.live", AgentID: aid, SessionID: sid, Data: live})
	}
	return events, nil
}
