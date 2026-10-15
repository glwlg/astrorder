package botgroups

import (
	"context"
	"errors"
	"path/filepath"

	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

type referenceController struct {
	controlledAgents
	fail bool
}

func (c *referenceController) ReadReply(_ context.Context, agent, session string, ids []string) (string, error) {
	if c.fail {
		return "", errors.New("native offline")
	}
	if agent != "a" || session != "native" || len(ids) != 1 || ids[0] != "answer" {
		return "", errors.New("incorrect native identity")
	}
	return "private native reply", nil
}
func TestGroupReplyPersistsReferencesAndReadsNativeContent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &referenceController{}
	pub := &publisher{}
	service, err := New(ctx, Options{Store: st, Controller: controller, Publisher: pub, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := st.SetPreference(ctx, "bot_group:g", Group{ID: "g", Members: []Member{{AgentID: "a", MachineID: "local"}}}, true); err != nil {
		t.Fatal(err)
	}
	message := Message{ID: "reply", GroupID: "g", SenderType: "agent", SenderID: "a", Text: "private native reply", SessionID: "native", NativeIDs: []string{"answer"}}
	if err := service.appendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_preferences WHERE value LIKE '%private native reply%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("native body persisted", count, err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE data LIKE '%private native reply%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("native body persisted in events", count, err)
	}
	messages, err := service.messages(ctx, "g")
	if err != nil || len(messages) != 1 || messages[0].Text != message.Text {
		t.Fatal("native reply not restored", messages, err)
	}
	if len(pub.events) != 1 || pub.events[0].Type != "bot_group.message" || pub.events[0].Cursor != 0 || pub.events[0].Data["text"] != message.Text {
		t.Fatal("live reply lost", pub.events)
	}
	controller.fail = true
	if _, err := service.messages(ctx, "g"); err == nil {
		t.Fatal("native history failure was hidden")
	}
}
