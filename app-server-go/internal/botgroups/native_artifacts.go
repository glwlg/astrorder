package botgroups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/sessions"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// NativeFileOperation is an executed operation from the reply's native turn.
// A command still needs semantic inspection: mentioning a file is not a write.
type NativeFileOperation struct {
	ID            string               `json:"id"`
	Kind          string               `json:"kind"`
	Path          string               `json:"path,omitempty"`
	Command       string               `json:"command,omitempty"`
	Cwd           string               `json:"cwd,omitempty"`
	ExitCode      *int                 `json:"exit_code,omitempty"`
	Output        string               `json:"native_output,omitempty"`
	ScriptSources []NativeScriptChange `json:"script_sources,omitempty"`
	scriptDiff    string
}

type artifactEvidenceReader interface {
	ReadArtifactEvidence(context.Context, string, string, []string) ([]NativeFileOperation, error)
}

func nativeFileOperations(raw map[string]any) []NativeFileOperation {
	item := raw
	if nested, ok := item["item"].(map[string]any); ok {
		item = nested
	}
	id, _ := item["id"].(string)
	if id == "" {
		return nil
	}
	switch item["type"] {
	case "fileChange":
		if item["status"] != "completed" {
			return nil
		}
		var out []NativeFileOperation
		changes, _ := item["changes"].([]any)
		for _, value := range changes {
			change, _ := value.(map[string]any)
			p, _ := change["path"].(string)
			kind, _ := change["kind"].(map[string]any)
			action, _ := kind["type"].(string)
			if action == "" {
				action, _ = change["kind"].(string)
			}
			if p == "" || (action != "add" && action != "update") {
				continue
			}
			if move, _ := kind["movePath"].(string); move != "" {
				p = move
			}
			diff, _ := change["diff"].(string)
			out = append(out, NativeFileOperation{ID: id, Kind: "fileChange", Path: p, scriptDiff: diff})
		}
		return out
	case "commandExecution":
		if item["status"] != "completed" && item["status"] != "failed" {
			return nil
		}
		code, err := strconv.Atoi(fmt.Sprint(item["exitCode"]))
		if err != nil {
			return nil
		}
		if item["status"] == "failed" && code == 0 {
			return nil
		}
		output, _ := item["aggregatedOutput"].(string)
		// A multi-command process can finish a write before a later command
		// fails. Only offer such records with their actual native output so
		// the classifier can demand an explicit completed-substep receipt.
		if code != 0 && output == "" {
			return nil
		}
		if code == 0 {
			// Successful execution is already attested by the exit code.
			// Read/test stdout can be enormous and is not write evidence.
			output = ""
		}
		if len(output) > 16*1024 {
			output = output[:16*1024] + "\n[truncated native output]"
		}
		command, _ := item["command"].(string)
		cwd, _ := item["cwd"].(string)
		if command == "" || len(command) > 64*1024 {
			return nil
		}
		// Codex's structured read/list/search actions are positive read-only
		// evidence and must never be offered to the model as possible writes.
		actions, _ := item["commandActions"].([]any)
		readOnly := len(actions) > 0
		for _, action := range actions {
			a, _ := action.(map[string]any)
			if a["type"] != "read" && a["type"] != "listFiles" && a["type"] != "search" {
				readOnly = false
			}
		}
		if !readOnly {
			return []NativeFileOperation{{ID: id, Kind: "commandExecution", Command: command, Cwd: cwd, ExitCode: &code, Output: output}}
		}
	}
	return nil
}

// ReadArtifactEvidence reads through the user boundary, not just the final
// assistant item. It never attaches, resumes, or sends to a native session.
func (c *NativeController) ReadArtifactEvidence(ctx context.Context, agentID, sessionID string, ids []string) ([]NativeFileOperation, error) {
	session, err := c.Store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil || c.Client == nil || c.Client.Runtime == nil {
		return nil, errors.New("群成员的原生文件操作记录不可用")
	}
	agent, err := c.Store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, errors.New("群成员 Agent 不存在")
	}
	connection, _ := session["connection_id"].(string)
	agentConnection, _ := agent["connection_id"].(string)
	if connectionID(connection) != connectionID(agentConnection) {
		return nil, errors.New("群会话的来源连接已变化")
	}
	workspace, _ := session["workspace"].(string)
	target, _, err := c.target(ctx, Member{AgentID: agentID, MachineID: connection, Workspace: &workspace})
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		return nil, errors.New("群回复缺少原生消息标识")
	}
	kind, _ := agent["kind"].(string)
	cursor, active := "", false
	seen := map[string]bool{}
	operations := []NativeFileOperation{}
	for {
		var items []map[string]any
		var next string
		if kind == "codex" || kind == "codex-ssh" {
			page, err := c.Client.ReadCodexHistoryPage(ctx, target, sessionID, cursor, 200)
			if err != nil {
				return nil, err
			}
			items = page.Data // Newest first.
			if page.NextCursor != nil {
				next = *page.NextCursor
			}
		} else {
			ref := &pb.SessionRef{SessionId: sessionID, AgentId: &agentID, ConnectionId: target.ConnectionId}
			request := &pb.HistoryPageRequest{Session: ref, Limit: 200}
			if cursor != "" {
				request.Before = &pb.NullableString{State: &pb.NullableString_Value{Value: cursor}}
			}
			page, err := c.Client.Runtime.GetHistoryPage(ctx, request)
			if err != nil {
				return nil, err
			}
			if page == nil || page.Session == nil || page.Session.SessionId != sessionID || page.Session.GetAgentId() != agentID || page.Session.GetConnectionId() != target.GetConnectionId() {
				return nil, errors.New("群会话历史来源不一致")
			}
			// The owned-history contract is chronological within each page.
			for i := len(page.Items) - 1; i >= 0; i-- {
				if page.Items[i] == nil {
					return nil, errors.New("原生群会话历史项无效")
				}
				var item map[string]any
				decoder := json.NewDecoder(bytes.NewReader(page.Items[i].Utf8Json))
				decoder.UseNumber()
				if err := decoder.Decode(&item); err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			if page.GetHasMore() {
				next = page.GetNextCursor().GetValue()
				if next == "" {
					return nil, errors.New("原生群会话历史游标无效")
				}
			}
		}
		for _, item := range items {
			for _, row := range sessions.ProjectNativeHistoryItem(item, agentID, sessionID, sessionID, agentID, kind) {
				if wanted[fmt.Sprint(row["id"])] && row["role"] == "assistant" && row["kind"] == "message" {
					delete(wanted, fmt.Sprint(row["id"]))
					active = true
				}
				if row["role"] == "user" {
					active = false
					if len(wanted) == 0 {
						return bindNativeScripts(operations, workspace), nil
					}
				}
			}
			if active {
				operations = append(operations, nativeFileOperations(item)...)
			}
		}
		if next == "" {
			// Without the native user boundary, the turn's evidence is incomplete.
			return nil, errors.New("未找到群回复所属轮次的完整原生文件操作记录")
		}
		if seen[next] {
			return nil, errors.New("原生群会话历史游标重复")
		}
		seen[next], cursor = true, next
	}
}

func evidenceForFile(file artifacts.File, operations []NativeFileOperation) []NativeFileOperation {
	out := []NativeFileOperation{}
	// Exact native file changes are stronger than incidental mentions in
	// shell commands and need no shell output or script context.
	for _, op := range operations {
		if op.Path == "" {
			continue
		}
		p := artifacts.Normalize(op.Path)
		if !artifacts.Absolute(p) {
			p = artifacts.Normalize(file.Workspace + "/" + p)
		}
		if artifacts.Equal(p, file.Path) {
			out = append(out, op)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, op := range operations {
		if op.Path != "" {
			continue
		}
		// Shell-created outputs need the executed command, an explicit literal
		// destination, and System One's write/delivery classification.
		command := commandPathText(op.Command)
		if len(file.Path) > 2 && file.Path[1] == ':' {
			command = strings.ToLower(command)
		}
		for _, p := range append([]string{file.Path}, file.Aliases...) {
			if !artifacts.Absolute(p) && op.Cwd != "" && !artifacts.Equal(artifacts.Normalize(op.Cwd+"/"+p), file.Path) {
				continue
			}
			literal := p
			if len(file.Path) > 2 && file.Path[1] == ':' {
				literal = strings.ToLower(p)
			}
			if literal != "" && strings.Contains(command, literal) {
				out = append(out, op)
				break
			}
		}
		if len(out) > 0 && out[len(out)-1].ID == op.ID {
			continue
		}
		for _, source := range op.ScriptSources {
			// A native patch to a script is not itself a file write. The
			// attached command must actually execute it; System One checks
			// the output destination and any partial-execution receipts.
			if strings.Contains(source.Diff, file.Name) {
				out = append(out, op)
				break
			}
		}
	}
	return out
}
