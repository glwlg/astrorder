package journal

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestOperationReservationSurvivesRestartAndConcurrentRetries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.db")
	s, err := OpenChecked(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, response, e := s.ReserveOperation("scope", "id", "digest")
			if e != nil {
				t.Error(e)
			}
			if fresh {
				winners.Add(1)
			}
			if len(response) != 0 {
				t.Error("unexpected response")
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("dispatch reservations=%d", winners.Load())
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenChecked(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fresh, response, err := s.ReserveOperation("scope", "id", "digest")
	if err != nil || fresh || len(response) != 0 {
		t.Fatalf("uncertain reservation lost: %v %s %v", fresh, response, err)
	}
	if _, _, err = s.ReserveOperation("scope", "id", "different"); err == nil {
		t.Fatal("conflicting payload accepted")
	}
	if err = s.CompleteOperation("scope", "id", []byte(`{"result":"native-confirmed"}`)); err != nil {
		t.Fatal(err)
	}
	fresh, response, err = s.ReserveOperation("scope", "id", "digest")
	if err != nil || fresh || string(response) != `{"result":"native-confirmed"}` {
		t.Fatalf("receipt lost: %v %s %v", fresh, response, err)
	}
}
