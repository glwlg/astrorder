package codex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNativeApprovalPreservesLargeNumericID(t *testing.T) {
	got := make(chan Notification, 1)
	r := New(Config{OnNotification: func(n Notification) { got <- n }})
	defer r.Close()
	r.readLoop(strings.NewReader(`{"id":9007199254740993,"method":"item/commandExecution/requestApproval","params":{"threadId":"s"}}`))
	select {
	case n := <-got:
		if n.ApprovalID != "codex:9007199254740993" {
			t.Fatalf("native approval identity corrupted: %s", n.ApprovalID)
		}
		raw, err := json.Marshal(n.Frame["id"])
		if err != nil || string(raw) != "9007199254740993" {
			t.Fatalf("raw id corrupted: %s %v", raw, err)
		}
	case <-time.After(time.Second):
		t.Fatal("approval not delivered")
	}
}
