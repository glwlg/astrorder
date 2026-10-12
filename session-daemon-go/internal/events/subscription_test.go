package events

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBeginSubscriptionValidatesCapacityAndUsesControlledDefault(t *testing.T) {
	j := New(4)
	defer j.Close()

	for _, capacity := range []int{-1, 65537} {
		result, err := j.BeginSubscription(capacity)
		if err == nil {
			t.Fatalf("capacity %d was accepted: %+v", capacity, result)
		}
	}
	if len(j.subscribers) != 0 {
		t.Fatalf("invalid requests registered subscribers: %d", len(j.subscribers))
	}

	result, err := j.BeginSubscription(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := cap(result.Subscription.frames); got != 256 {
		t.Fatalf("default capacity = %d, want 256", got)
	}
	j.Unsubscribe(result.Subscription)

	result, err = j.BeginSubscription(65536)
	if err != nil {
		t.Fatal(err)
	}
	if got := cap(result.Subscription.frames); got != 65536 {
		t.Fatalf("maximum capacity = %d, want 65536", got)
	}
	j.Unsubscribe(result.Subscription)
}

func TestBeginSubscriptionCapturesCompleteCatalogueAndFixedReplayBoundary(t *testing.T) {
	j, err := Open(t.TempDir()+"/events.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	const sessionCount = 260
	for i := 0; i < sessionCount; i++ {
		if err = j.TrackSession(fmt.Sprintf("catalog-%03d", i), "idle"); err != nil {
			t.Fatal(err)
		}
	}
	const replaySession = "catalog-129"
	for i := 0; i < 5; i++ {
		if _, err = j.Commit(replaySession, "token", map[string]any{"n": i + 1}, "running"); err != nil {
			t.Fatal(err)
		}
	}

	begin, err := j.BeginSubscription(8)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Unsubscribe(begin.Subscription)
	if len(begin.Sessions) != sessionCount {
		t.Fatalf("catalogue contains %d sessions, want %d", len(begin.Sessions), sessionCount)
	}
	if empty := begin.Sessions["catalog-259"]; empty != (SnapshotSession{Status: "idle"}) {
		t.Fatalf("empty stream snapshot = %+v", empty)
	}
	boundary := begin.Sessions[replaySession]
	if boundary.Status != "running" || boundary.MinSeqID != 1 || boundary.MaxSeqID != 5 {
		t.Fatalf("replay boundary = %+v", boundary)
	}

	if _, err = j.Commit(replaySession, "token", map[string]any{"n": 6}, "running"); err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("created-after-snapshot", "idle"); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Commit("created-after-snapshot", "started", map[string]any{}, "running"); err != nil {
		t.Fatal(err)
	}
	if _, exists := begin.Sessions["created-after-snapshot"]; exists {
		t.Fatal("fixed snapshot changed after a new session was committed")
	}
	if got := begin.Sessions[replaySession].MaxSeqID; got != 5 {
		t.Fatalf("fixed high watermark changed to %d", got)
	}

	seen := map[string]bool{}
	after := int64(0)
	for {
		page, pageErr := j.Page(replaySession, after, boundary.MaxSeqID, 2)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		for _, frame := range page.Frames {
			key := fmt.Sprintf("%s/%d", frame.SessionID, frame.SeqID)
			if seen[key] {
				t.Fatalf("duplicate replay frame %s", key)
			}
			seen[key] = true
		}
		after = page.Next
		if !page.HasMore {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatalf("replayed %d frames through fixed boundary, want 5", len(seen))
	}

	for i := 0; i < 2; i++ {
		select {
		case frame := <-begin.Subscription.Frames():
			key := fmt.Sprintf("%s/%d", frame.SessionID, frame.SeqID)
			if seen[key] {
				t.Fatalf("frame crossed replay/live boundary twice: %s", key)
			}
			seen[key] = true
		case <-time.After(time.Second):
			t.Fatal("post-boundary frame was not queued")
		}
	}
	if !seen[replaySession+"/6"] || !seen["created-after-snapshot/1"] {
		t.Fatalf("post-boundary or new-session frame missing: %v", seen)
	}
}

func TestBeginSubscriptionSerializesCommittedButUnpublishedFrame(t *testing.T) {
	j, err := Open(t.TempDir()+"/events.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	// Reproduce the interval after AppendBatch commits but before commitLoop can
	// publish under j.mu. The read-side primitive must not put the same frame in
	// both its durable snapshot and its live queue.
	j.commitGate.RLock()
	gateHeld := true
	defer func() {
		if gateHeld {
			j.commitGate.RUnlock()
		}
	}()
	j.mu.Lock()
	muHeld := true
	defer func() {
		if muHeld {
			j.mu.Unlock()
		}
	}()

	type beginResult struct {
		value ReplaySubscription
		err   error
	}
	beginDone := make(chan beginResult, 1)
	beginStarted := make(chan struct{})
	go func() {
		close(beginStarted)
		value, beginErr := j.BeginSubscription(4)
		beginDone <- beginResult{value: value, err: beginErr}
	}()
	<-beginStarted
	// Put a would-be implementation that waits only on j.mu ahead of the
	// publisher. A correct implementation waits for the commit gate instead.
	time.Sleep(20 * time.Millisecond)

	request := commitRequest{
		frame: Frame{
			Timestamp: float64(time.Now().UnixMicro()) / 1e6,
			SessionID: "gap",
			Event:     "token",
			Payload:   map[string]any{"text": "committed"},
			Status:    "running",
		},
		done: make(chan commitResult, 1),
	}
	j.commitQueue <- request

	deadline := time.Now().Add(2 * time.Second)
	for {
		latest, latestErr := j.store.Latest(j.identity)
		if latestErr != nil {
			j.mu.Unlock()
			muHeld = false
			j.commitGate.RUnlock()
			gateHeld = false
			t.Fatal(latestErr)
		}
		committed := false
		for _, frame := range latest {
			if frame.SessionID == "gap" && frame.SeqID == 1 {
				committed = true
				break
			}
		}
		if committed {
			break
		}
		if time.Now().After(deadline) {
			j.mu.Unlock()
			muHeld = false
			j.commitGate.RUnlock()
			gateHeld = false
			t.Fatal("frame did not become durable while publication was excluded")
		}
		runtime.Gosched()
	}

	j.mu.Unlock()
	muHeld = false
	var committed commitResult
	select {
	case committed = <-request.done:
	case <-time.After(2 * time.Second):
		j.commitGate.RUnlock()
		gateHeld = false
		t.Fatal("commit loop did not publish the durable frame")
	}
	j.commitGate.RUnlock()
	gateHeld = false
	if committed.err != nil || committed.frame.SeqID != 1 {
		t.Fatalf("commit result = %+v", committed)
	}

	var result beginResult
	select {
	case result = <-beginDone:
	case <-time.After(2 * time.Second):
		t.Fatal("BeginSubscription deadlocked with commit publication")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer j.Unsubscribe(result.value.Subscription)
	if got := result.value.Sessions["gap"].MaxSeqID; got != 1 {
		t.Fatalf("snapshot high watermark = %d, want 1", got)
	}
	select {
	case duplicate := <-result.value.Subscription.Frames():
		t.Fatalf("committed frame appeared in snapshot and live queue: %+v", duplicate)
	default:
	}

	second, err := j.Commit("gap", "token", map[string]any{"text": "live"}, "running")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case live := <-result.value.Subscription.Frames():
		if live.SeqID != second.SeqID || live.SeqID != 2 {
			t.Fatalf("wrong live continuation: %+v", live)
		}
	case <-time.After(time.Second):
		t.Fatal("post-boundary live frame was lost")
	}
}

func TestBeginSubscriptionConcurrentNewSessionAppearsExactlyOnce(t *testing.T) {
	j, err := Open(t.TempDir()+"/events.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	for i := 0; i < 32; i++ {
		sessionID := fmt.Sprintf("concurrent-%02d", i)
		start := make(chan struct{})
		type beginResult struct {
			value ReplaySubscription
			err   error
		}
		beginDone := make(chan beginResult, 1)
		commitDone := make(chan error, 1)
		go func() {
			<-start
			value, beginErr := j.BeginSubscription(4)
			beginDone <- beginResult{value: value, err: beginErr}
		}()
		go func() {
			<-start
			_, commitErr := j.Commit(sessionID, "started", map[string]any{}, "running")
			commitDone <- commitErr
		}()
		close(start)

		var begin beginResult
		select {
		case begin = <-beginDone:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent subscription timed out")
		}
		select {
		case err = <-commitDone:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent commit timed out")
		}
		if begin.err != nil || err != nil {
			t.Fatalf("begin=%v commit=%v", begin.err, err)
		}

		count := 0
		if boundary, exists := begin.value.Sessions[sessionID]; exists {
			page, pageErr := j.Page(sessionID, 0, boundary.MaxSeqID, 10)
			if pageErr != nil {
				t.Fatal(pageErr)
			}
			count += len(page.Frames)
		}
		for {
			select {
			case frame := <-begin.value.Subscription.Frames():
				if frame.SessionID == sessionID && frame.SeqID == 1 {
					count++
				}
			default:
				goto drained
			}
		}
	drained:
		j.Unsubscribe(begin.value.Subscription)
		if count != 1 {
			t.Fatalf("iteration %d delivered concurrent first frame %d times", i, count)
		}
	}
}

func TestBeginSubscriptionPreservesExpiredCursorMetadata(t *testing.T) {
	j, err := Open(t.TempDir()+"/events.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.ConfigureRetention(2048, 600, 3600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err = j.Commit("expired", "text", map[string]any{"text": strings.Repeat("x", 200)}, "idle"); err != nil {
			t.Fatal(err)
		}
	}

	begin, err := j.BeginSubscription(4)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Unsubscribe(begin.Subscription)
	boundary := begin.Sessions["expired"]
	if boundary.MinSeqID <= 1 || boundary.MaxSeqID != 6 {
		t.Fatalf("expired boundary metadata = %+v", boundary)
	}
	page, err := j.Page("expired", 0, boundary.MaxSeqID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Frames) != 0 || page.MinSeq != boundary.MinSeqID || page.Through != boundary.MaxSeqID {
		t.Fatalf("expired cursor semantics were hidden: page=%+v snapshot=%+v", page, boundary)
	}
}

func TestBeginSubscriptionOverflowDoesNotBlockHealthySubscriberOrControl(t *testing.T) {
	j := New(8)
	defer j.Close()
	slow, err := j.BeginSubscription(1)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Unsubscribe(slow.Subscription)
	fast, err := j.BeginSubscription(8)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Unsubscribe(fast.Subscription)

	for i := 1; i <= 3; i++ {
		j.Publish("stream", "token", map[string]any{"n": i}, "running")
	}
	if slow.Subscription.Healthy() {
		t.Fatal("overflowing subscriber stayed healthy after a frame could not be queued")
	}
	for seq := int64(1); seq <= 3; seq++ {
		select {
		case frame := <-fast.Subscription.Frames():
			if frame.SeqID != seq {
				t.Fatalf("healthy subscriber order = %d, want %d", frame.SeqID, seq)
			}
		case <-time.After(time.Second):
			t.Fatal("healthy subscriber was blocked by slow subscriber")
		}
	}

	controlDone := make(chan error, 1)
	go func() { controlDone <- j.TrackSession("control", "idle") }()
	select {
	case err = <-controlDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("overflowing subscriber blocked journal control")
	}
}

func TestBeginSubscriptionPropagatesStorageFailureAndDetaches(t *testing.T) {
	j, err := Open(t.TempDir()+"/events.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("known", "idle"); err != nil {
		t.Fatal(err)
	}
	if err = j.store.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := j.BeginSubscription(4)
	if err == nil {
		t.Fatalf("closed storage returned stale snapshot: %+v", result)
	}
	j.mu.Lock()
	subscriberCount := len(j.subscribers)
	j.mu.Unlock()
	if subscriberCount != 0 {
		t.Fatalf("failed snapshot leaked %d subscriber(s)", subscriberCount)
	}
	if j.CheckAdmission() == nil || j.Health()["degraded"] != true {
		t.Fatal("snapshot storage failure did not degrade journal health")
	}
	_ = j.Close()
}

func TestBeginSubscriptionIsSafeAgainstCloseRace(t *testing.T) {
	for i := 0; i < 16; i++ {
		j, err := Open(t.TempDir()+fmt.Sprintf("/events-%02d.db", i), 4)
		if err != nil {
			t.Fatal(err)
		}
		if err = j.TrackSession("known", "idle"); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		type beginResult struct {
			value ReplaySubscription
			err   error
		}
		beginDone := make(chan beginResult, 1)
		closeDone := make(chan error, 1)
		go func() {
			<-start
			value, beginErr := j.BeginSubscription(4)
			beginDone <- beginResult{value: value, err: beginErr}
		}()
		go func() {
			<-start
			closeDone <- j.Close()
		}()
		close(start)

		var begin beginResult
		select {
		case begin = <-beginDone:
		case <-time.After(2 * time.Second):
			t.Fatal("BeginSubscription deadlocked with Close")
		}
		select {
		case err = <-closeDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close deadlocked with BeginSubscription")
		}
		if begin.err == nil {
			if begin.value.Subscription == nil || begin.value.Sessions["known"].Status != "idle" {
				t.Fatalf("successful racing snapshot was incomplete: %+v", begin.value)
			}
			j.Unsubscribe(begin.value.Subscription)
		}
		if _, err = j.BeginSubscription(4); err == nil {
			t.Fatal("subscription began after journal close")
		}
	}
}

func TestBeginSubscriptionRestoresDurableCatalogueAfterReopen(t *testing.T) {
	path := t.TempDir() + "/events.db"
	j, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("empty", "idle"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = j.Commit("durable", "token", map[string]any{"n": i + 1}, "running"); err != nil {
			t.Fatal(err)
		}
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}

	j, err = Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	begin, err := j.BeginSubscription(4)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Unsubscribe(begin.Subscription)
	if empty := begin.Sessions["empty"]; empty != (SnapshotSession{Status: "idle"}) {
		t.Fatalf("empty durable session = %+v", empty)
	}
	if durable := begin.Sessions["durable"]; durable.Status != "running" || durable.MinSeqID != 1 || durable.MaxSeqID != 3 {
		t.Fatalf("durable stream after reopen = %+v", durable)
	}
}

func TestBeginSubscriptionConcurrentCallsHaveIndependentQueues(t *testing.T) {
	j := New(4)
	defer j.Close()
	const count = 16
	results := make(chan ReplaySubscription, count)
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := j.BeginSubscription(2)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	registered := make([]ReplaySubscription, 0, count)
	for result := range results {
		registered = append(registered, result)
	}
	if len(registered) != count {
		t.Fatalf("registered %d subscriptions, want %d", len(registered), count)
	}
	for _, result := range registered {
		j.Unsubscribe(result.Subscription)
	}
}
