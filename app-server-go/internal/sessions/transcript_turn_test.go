package sessions

import "testing"

func TestCodexHistoryPreservesNativeTurnIdentity(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		rows := projectHistoryItem(map[string]any{"turnId": "previous-turn", "item": map[string]any{"id": "intro", "type": "agentMessage", "text": "fixture"}}, "a", "s", "s", "source", kind)
		if len(rows) != 1 || rows[0]["turn_id"] != "previous-turn" {
			t.Fatalf("%s lost native turn: %#v", kind, rows)
		}
	}
}
