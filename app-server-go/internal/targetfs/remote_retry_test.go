package targetfs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRemoteStartRetryHelperProcess stands in for `ssh host helper`: it fails the
// way sshd does when it is dropping connections, until it has failed
// GO_F07_FAILURES times (counted in a file shared across processes).
func TestRemoteStartRetryHelperProcess(t *testing.T) {
	counter := os.Getenv("GO_F07_COUNTER")
	if counter == "" {
		return
	}
	data, _ := os.ReadFile(counter)
	failures := len(data)
	limit := 0
	for _, c := range os.Getenv("GO_F07_FAILURES") {
		limit = limit*10 + int(c-'0')
	}
	if failures < limit {
		file, _ := os.OpenFile(counter, os.O_APPEND|os.O_WRONLY, 0o600)
		file.WriteString("x")
		file.Close()
		os.Stderr.WriteString("kex_exchange_identification: Connection closed by remote host\n")
		os.Exit(255)
	}
	if err := ServeHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func retryFixture(t *testing.T, failures string) (*remoteFileSystem, string, string) {
	t.Helper()
	root := t.TempDir()
	counter := filepath.Join(t.TempDir(), "failures")
	if err := os.WriteFile(counter, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewRemote(RemoteOptions{
		Workspace: root, AllowedRoots: []string{root},
		Command:     []string{os.Args[0], "-test.run=TestRemoteStartRetryHelperProcess", "--", t.Name()},
		Environment: []string{"GO_F07_COUNTER=" + counter, "GO_F07_FAILURES=" + failures},
	})
	if err != nil {
		t.Fatal(err)
	}
	return filesystem.(*remoteFileSystem), root, counter
}

func TestRemoteRetriesWhenSSHRefusesTheConnection(t *testing.T) {
	filesystem, root, _ := retryFixture(t, "2")
	if _, err := filesystem.List(context.Background(), ListRequest{Path: root, Depth: 1}); err != nil {
		t.Fatalf("two refused connections should be retried: %v", err)
	}
}

func TestRemoteGivesUpAfterTheRetryBudget(t *testing.T) {
	filesystem, root, counter := retryFixture(t, "9")
	_, err := filesystem.List(context.Background(), ListRequest{Path: root, Depth: 1})
	if err == nil {
		t.Fatal("a target that keeps refusing must still fail")
	}
	if data, _ := os.ReadFile(counter); len(data) != remoteStartRetries {
		t.Fatalf("helper was started %d times, want %d", len(data), remoteStartRetries)
	}
}

func TestRemoteStartsAreBoundedPerTarget(t *testing.T) {
	filesystem, root, _ := retryFixture(t, "0")
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	slot := filesystem.slot()
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot <- struct{}{}
			now := running.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			running.Add(-1)
			<-slot
		}()
	}
	wg.Wait()
	if peak.Load() > remoteStartSlots {
		t.Fatalf("%d starts ran at once, limit is %d", peak.Load(), remoteStartSlots)
	}
	if _, err := filesystem.List(context.Background(), ListRequest{Path: root, Depth: 1}); err != nil {
		t.Fatal(err)
	}
}
