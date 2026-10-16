package codex

import (
	"encoding/json"
	"errors"
	"fmt"
)

type nativeRPCError struct {
	code    string
	message string
	raw     any
}

func (e *nativeRPCError) Error() string { return fmt.Sprintf("codex RPC error: %v", e.raw) }
func newNativeRPCError(raw any) error {
	object, _ := raw.(map[string]any)
	code, _ := object["code"].(json.Number)
	message, _ := object["message"].(string)
	return &nativeRPCError{code: string(code), message: message, raw: raw}
}
func emptyHistoryUnsupported(err error) bool {
	var rpc *nativeRPCError
	return errors.As(err, &rpc) && rpc.code == "-32601" && (rpc.message == "thread/items/list is not supported yet" || rpc.message == "list_turns is not supported yet")
}
