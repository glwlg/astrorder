package codex

import (
	"context"
	"encoding/json"
	"testing"
)

func TestUserInputRequestWaitsForExplicitAnswer(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "running")
	frames := make(chan map[string]any, 4)
	r.stdin = captureWriter{frames}
	r.handleNotification("item/tool/requestUserInput", map[string]any{"id": json.Number("9007199254740993"), "params": map[string]any{"threadId": "thread", "turnId": "turn", "itemId": "item", "isBlocking": true, "questions": []any{map[string]any{"id": "name", "header": "姓名", "question": "你叫什么名字？"}}}})
	select {
	case frame := <-frames:
		t.Fatalf("question automatically rejected: %v", frame)
	default:
	}
	if r.Status() != "waiting_approval" {
		t.Fatalf("status: %s", r.Status())
	}
	invalid := Command{Action: "session.approve", SessionID: "thread", ApprovalID: "codex:9007199254740993", Decision: "accept"}
	if _, err := r.Command(context.Background(), invalid); err == nil {
		t.Fatal("implicit answer accepted")
	}
	invalid.Options = map[string]any{"answers": map[string]any{"foreign": map[string]any{"answers": []any{"x"}}}}
	if _, err := r.Command(context.Background(), invalid); err == nil {
		t.Fatal("foreign question accepted")
	}
	select {
	case frame := <-frames:
		t.Fatalf("invalid answer reached native peer: %v", frame)
	default:
	}
	cmd := Command{Action: "session.approve", SessionID: "thread", ApprovalID: "codex:9007199254740993", Decision: "accept", Options: map[string]any{"answers": map[string]any{"name": map[string]any{"answers": []any{"张三"}}}}}
	if _, err := r.Command(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	frame := <-frames
	if frame["id"].(float64) == 0 {
		t.Fatal(frame)
	}
	result := frame["result"].(map[string]any)
	if result["decision"] != nil || result["answers"].(map[string]any)["name"].(map[string]any)["answers"].([]any)[0] != "张三" {
		t.Fatal(frame)
	}
	if _, err := r.Command(context.Background(), cmd); err == nil {
		t.Fatal("duplicate answer sent")
	}
	r.handleNotification("serverRequest/resolved", map[string]any{"params": map[string]any{"threadId": "thread", "requestId": json.Number("9007199254740993")}})
}
