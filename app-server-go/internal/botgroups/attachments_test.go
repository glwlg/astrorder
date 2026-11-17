package botgroups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestGroupImagesFollowDeferredUnreadMessages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "image", "name": "截图.png", "media_type": "image/png", "storage_name": "image.blob", "size": 4}); err != nil {
		t.Fatal(err)
	}
	controller := &stagedController{started: make(chan Turn, 4), replies: map[string]chan Reply{
		"design": make(chan Reply, 1), "dev": make(chan Reply, 1),
	}, texts: map[string]string{}}
	decider := decisionFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		designed := false
		for _, message := range input.Messages {
			if message.SenderID == "design" {
				designed = true
			}
		}
		result := Decision{Send: map[string]bool{}, Complete: true}
		for _, candidate := range input.Candidates {
			result.Send[candidate.MemberID] = candidate.MemberID == "design" && !designed || candidate.MemberID == "dev" && designed
			for _, message := range candidate.Unread {
				if message.SenderType == "user" && message.ImageCount != 1 {
					t.Error("System One did not receive image metadata")
				}
			}
		}
		return result, nil
	})
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: decider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name":    json.RawMessage(`"图片讨论"`),
		"members": json.RawMessage(`[{"id":"design","agent_id":"a","machine_id":"local","alias":"设计"},{"id":"dev","agent_id":"a","machine_id":"local","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A picture alone is a valid user message.
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`""`), "attachment_ids": json.RawMessage(`["image"]`)}); err != nil {
		t.Fatal(err)
	}
	readTurn := func(want string) Turn {
		t.Helper()
		select {
		case turn := <-controller.started:
			if memberID(turn.Member) != want || len(turn.Attachments) != 1 || turn.Attachments[0]["id"] != "image" || !strings.Contains(turn.Prompt, "截图.png") {
				t.Fatalf("missing or misrouted image: %+v", turn)
			}
			return turn
		case <-time.After(3 * time.Second):
			t.Fatal("member did not receive message")
			return Turn{}
		}
	}
	readTurn("design")
	controller.replies["design"] <- Reply{Text: "@开发 请按图片处理", NativeIDs: []string{"design-reply"}}
	turn := readTurn("dev")
	if !strings.Contains(turn.Prompt, "@开发 请按图片处理") {
		t.Fatal("deferred delivery lost the designer's message")
	}
	history, err := service.messages(ctx, group.ID)
	if err != nil || len(history) < 2 || len(history[0].Attachments) != 1 || history[0].Attachments[0]["url"] != "/api/v1/attachments/image" {
		t.Fatalf("image history lost: %+v %v", history, err)
	}
}

func TestGroupImageValidationDoesNotQueueInvalidMessages(t *testing.T) {
	st, service := forkFixture(t)
	ctx := context.Background()
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "text", "name": "file.txt", "media_type": "text/plain", "storage_name": "text.blob", "size": 1}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`["missing"]`, `["text"]`, `["text","text"]`, `[{}]`, `[""]`} {
		if _, err := service.messageAttachments(ctx, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid attachment ids %s", raw)
		}
	}
}
