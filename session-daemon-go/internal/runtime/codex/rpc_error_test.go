package codex

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestEmptyHistoryUnsupportedRequiresExactNativeCodeAndMessage(t *testing.T) {
	for _, test := range []struct {
		code    string
		message string
		want    bool
	}{
		{"-32601", "list_turns is not supported yet", true},
		{"-32601", "thread/items/list is not supported yet", true},
		{"-32000", "list_turns is not supported yet", false},
		{"-32601", "storage unavailable", false},
		{"-32600", "thread not loaded", false},
	} {
		err := newNativeRPCError(map[string]any{"code": json.Number(test.code), "message": test.message})
		if emptyHistoryUnsupported(fmt.Errorf("wrapped: %w", err)) != test.want {
			t.Fatal(test)
		}
	}
	if emptyHistoryUnsupported(fmt.Errorf("list_turns is not supported yet")) {
		t.Fatal("unstructured error accepted")
	}
}
