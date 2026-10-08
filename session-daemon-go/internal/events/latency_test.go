package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in measured synthetic load, never represented as native model performance.
func TestDurableConcurrentLatency(t *testing.T) {
	output := os.Getenv("ASTRORDER_LATENCY_OUTPUT")
	if output == "" {
		t.Skip("set ASTRORDER_LATENCY_OUTPUT for raw latency samples")
	}
	j, err := Open(filepath.Join(t.TempDir(), "latency.db"), 128)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	const sessions, perSession = 16, 128
	healthy := j.SubscribeWithCapacity(sessions * perSession)
	slow := j.SubscribeWithCapacity(1)
	samples := make(chan float64, sessions*perSession)
	statusSamples := make(chan float64, sessions*perSession)
	errors := make(chan error, sessions)
	var wg sync.WaitGroup
	for s := 0; s < sessions; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			id := fmt.Sprintf("load-%d", s)
			for n := 0; n < perSession; n++ {
				started := time.Now()
				_, err := j.Commit(id, "synthetic.native", map[string]any{"text": strings.Repeat("x", 1024)}, "running")
				if err != nil {
					errors <- err
					return
				}
				samples <- float64(time.Since(started).Microseconds()) / 1000
				started = time.Now()
				j.Statuses()
				statusSamples <- float64(time.Since(started).Microseconds()) / 1000
			}
		}(s)
	}
	wg.Wait()
	close(samples)
	close(statusSamples)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	commits, status := []float64{}, []float64{}
	for v := range samples {
		commits = append(commits, v)
	}
	for v := range statusSamples {
		status = append(status, v)
	}
	if slow.Healthy() {
		t.Fatal("non-reading subscriber was not evicted")
	}
	if !healthy.Healthy() || len(healthy.frames) != sessions*perSession {
		t.Fatal("healthy subscriber lost events")
	}
	percentile := func(v []float64, p int) float64 {
		sorted := append([]float64(nil), v...)
		sort.Float64s(sorted)
		return sorted[(len(sorted)*p+99)/100-1]
	}
	report := map[string]any{"scope": "synthetic durable commit to subscriber queue; status snapshot, not websocket RTT or browser paint", "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "sessions": sessions, "events_per_session": perSession, "payload_bytes": 1024, "commit_ms": commits, "status_ms": status, "commit_p95_ms": percentile(commits, 95), "commit_p99_ms": percentile(commits, 99), "status_p95_ms": percentile(status, 95)}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("commit p95=%.3fms p99=%.3fms; status p95=%.3fms; samples=%d; slow subscriber evicted", percentile(commits, 95), percentile(commits, 99), percentile(status, 95), len(commits))
	if percentile(commits, 95) > 20 || percentile(commits, 99) > 100 || percentile(status, 95) > 100 {
		t.Fatal("design latency budget exceeded; inspect raw samples")
	}
}
