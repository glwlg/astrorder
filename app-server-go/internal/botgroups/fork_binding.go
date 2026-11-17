package botgroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func memberBindingKey(groupID string, member Member) string {
	return "group_session:" + groupID + ":" + memberID(member)
}

func groupBindingExistsTx(ctx context.Context, tx *sql.Tx, groupID, id string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspace_preferences WHERE key=?`, "group_session:"+groupID+":"+id).Scan(&count)
	return count > 0, err
}

func memberValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func sameMemberSession(a, b Member) bool {
	return a.AgentID == b.AgentID && a.MachineID == b.MachineID &&
		memberValue(a.Workspace) == memberValue(b.Workspace) &&
		memberValue(a.SourceSessionID) == memberValue(b.SourceSessionID) &&
		memberValue(a.ForkSessionID) == memberValue(b.ForkSessionID)
}

func connectionID(value string) string {
	if value == "" || value == "local" {
		return "local"
	}
	return value
}

// bindForkTx binds only a confirmed child produced by the existing native fork API.
// The source session is never attached, sent to, or assigned to the group.
func bindForkTx(ctx context.Context, tx *sql.Tx, groupID string, member Member) error {
	if member.SourceSessionID == nil && member.ForkSessionID == nil {
		return nil
	}
	source, child := memberValue(member.SourceSessionID), memberValue(member.ForkSessionID)
	if source == "" || child == "" || source == child {
		return errors.New("分叉会话身份无效")
	}
	var sourceWorkspace, sourceConnection sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT workspace, connection_id FROM sessions WHERE agent_id=? AND id=?`, member.AgentID, source).Scan(&sourceWorkspace, &sourceConnection); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("来源会话不存在或不属于该 Agent")
		}
		return err
	}
	var parentAgent, parentSession, childWorkspace, childConnection, control, status, nativeID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT handoff_from_agent_id, handoff_from_session_id, workspace, connection_id, control_state, status, source_session_id FROM sessions WHERE agent_id=? AND id=?`, member.AgentID, child).Scan(&parentAgent, &parentSession, &childWorkspace, &childConnection, &control, &status, &nativeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("分叉会话不存在或不属于该 Agent")
		}
		return err
	}
	if parentAgent.String != member.AgentID || parentSession.String != source || control.String != "owned" || status.String != "idle" || nativeID.String != child {
		return errors.New("分叉会话身份或状态未确认")
	}
	if connectionID(sourceConnection.String) != connectionID(childConnection.String) || connectionID(childConnection.String) != connectionID(member.MachineID) {
		return errors.New("分叉会话与群成员的连接不一致")
	}
	if strings.TrimSpace(sourceWorkspace.String) == "" || strings.TrimSpace(sourceWorkspace.String) != strings.TrimSpace(childWorkspace.String) || memberValue(member.Workspace) != strings.TrimSpace(childWorkspace.String) {
		return errors.New("分叉成员必须使用来源会话的工作区")
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT key FROM workspace_preferences WHERE key LIKE 'group_session:%' AND json_extract(value, '$.agent_id')=? AND json_extract(value, '$.session_id')=? LIMIT 1`, member.AgentID, child).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existing != "" && existing != memberBindingKey(groupID, member) {
		return errors.New("该分叉会话已经加入其他群成员")
	}
	scopeKey := "blackboard_scope:" + member.AgentID + "::" + child
	var scopeRaw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, scopeKey).Scan(&scopeRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var scope string
		if json.Unmarshal([]byte(scopeRaw), &scope) != nil || scope != "group::"+groupID {
			return errors.New("该分叉会话已有其他黑板作用域")
		}
	}
	if err := putTx(ctx, tx, memberBindingKey(groupID, member), map[string]any{
		"agent_id": member.AgentID, "session_id": child, "connection_id": childConnection.String,
	}); err != nil {
		return err
	}
	return putTx(ctx, tx, scopeKey, "group::"+groupID)
}

func unbindMemberTx(ctx context.Context, tx *sql.Tx, groupID, id string) error {
	key := "group_session:" + groupID + ":" + id
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var binding struct {
			AgentID   string `json:"agent_id"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(raw), &binding); err != nil {
			return err
		}
		scope, _ := json.Marshal("group::" + groupID)
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=? AND value=?`, "blackboard_scope:"+binding.AgentID+"::"+binding.SessionID, string(scope)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE key=?`, key)
	return err
}
