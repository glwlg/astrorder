package botgroups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type timeoutRecoveryController interface {
	RecoverTimeout(context.Context, Group, Member, Message, Message) (Reply, error)
}

// RecoverTimeout correlates the exact old group prompt to one completed
// command and one native final reply. It does not send or replay a command.
func (c *NativeController) RecoverTimeout(ctx context.Context, group Group, member Member, user, failure Message) (Reply, error) {
	sessionID := failure.SessionID
	if sessionID == "" {
		return Reply{}, errors.New("超时记录缺少群成员会话")
	}
	binding, err := c.Store.GetPreference(ctx, "group_session:"+group.ID+":"+memberID(member))
	if err != nil {
		return Reply{}, err
	}
	value, ok := binding.Value.(map[string]any)
	if !binding.Exists || !ok || value["session_id"] != sessionID || value["agent_id"] != member.AgentID {
		return Reply{}, errors.New("群成员会话绑定未确认")
	}
	start, err := store.ParseTime(user.CreatedAt)
	if err != nil {
		return Reply{}, errors.New("群消息时间无效")
	}
	end, err := store.ParseTime(failure.CreatedAt)
	if err != nil || end.Before(start) {
		return Reply{}, errors.New("超时记录时间无效")
	}
	commands, err := c.Store.QueryRows(ctx, "SELECT id, action, state, text, created_at FROM commands WHERE agent_id=? AND session_id=? ORDER BY row_id", member.AgentID, sessionID)
	if err != nil {
		return Reply{}, err
	}
	var prompt string
	matches := 0
	for _, command := range commands {
		id, _ := command["id"].(string)
		text, _ := command["text"].(string)
		at, timeErr := store.ParseTime(command["created_at"])
		if timeErr != nil || !strings.HasPrefix(id, "cmd-g-") || command["action"] != "send" ||
			at.Before(start) || at.After(end) || !strings.Contains(text, "共享黑板 session_key=group::"+group.ID) ||
			!strings.HasSuffix(text, "当前任务："+user.Text) {
			continue
		}
		if command["state"] != "completed" {
			return Reply{}, errors.New("原会话任务尚未确认完成")
		}
		prompt = text
		matches++
	}
	if matches != 1 {
		return Reply{}, errors.New("无法唯一确认超时对应的原会话任务")
	}
	target, _, err := c.target(ctx, member)
	if err != nil {
		return Reply{}, err
	}
	ref := &pb.SessionRef{SessionId: sessionID, AgentId: &member.AgentID, ConnectionId: target.ConnectionId}
	history, err := c.historyPages(ctx, ref, nil, prompt)
	if err != nil {
		return Reply{}, err
	}
	found := false
	var result Reply
	for _, item := range history {
		if item["role"] == "user" && item["text"] == prompt {
			if found {
				return Reply{}, errors.New("原会话任务出现多次，无法安全恢复")
			}
			found = true
			continue
		}
		if !found {
			continue
		}
		if item["role"] == "user" {
			break
		}
		if item["role"] != "assistant" || item["kind"] != "message" || item["group_nonfinal"] == true {
			continue
		}
		text, _ := item["text"].(string)
		id, _ := item["id"].(string)
		if strings.TrimSpace(text) == "" || id == "" {
			continue
		}
		if len(result.NativeIDs) != 0 {
			return Reply{}, errors.New("原会话回复不唯一，无法安全恢复")
		}
		result = Reply{Text: text, NativeIDs: []string{id}}
		result.CreatedAt, _ = item["created_at"].(string)
	}
	if !found || len(result.NativeIDs) != 1 {
		return Reply{}, errors.New("原会话的最终回复尚不可用")
	}
	if _, err := store.ParseTime(result.CreatedAt); err != nil {
		return Reply{}, errors.New("原会话回复时间无效")
	}
	return result, nil
}

type recoveredTimeout struct {
	member  Member
	user    Message
	failure Message
	reply   Reply
}

func (s *Service) recoverTimedOut(ctx context.Context, id string) (Group, error) {
	controller, ok := s.options.Controller.(timeoutRecoveryController)
	if !ok {
		return Group{}, &serviceError{503, errors.New("原会话回复恢复不可用")}
	}
	s.submissionMu.Lock()
	group, err := s.get(ctx, id)
	if err != nil {
		s.submissionMu.Unlock()
		return Group{}, err
	}
	messages, err := s.messages(ctx, id)
	if err != nil {
		s.submissionMu.Unlock()
		return Group{}, err
	}
	items := []recoveredTimeout{}
	for _, member := range group.Members {
		mid := memberID(member)
		state := group.MemberStates[mid]
		if state.Status != "failed" || state.LastError != "context deadline exceeded" {
			continue
		}
		for i := len(messages) - 1; i >= 0; i-- {
			failure := messages[i]
			if failure.SenderType != "system" || failure.SenderMemberID != mid || failure.Text != "执行失败：context deadline exceeded" || failure.SessionID != state.SessionID {
				continue
			}
			for j := i - 1; j >= 0; j-- {
				if messages[j].SenderType == "user" {
					items = append(items, recoveredTimeout{member: member, user: messages[j], failure: failure})
					break
				}
			}
			break
		}
	}
	if len(items) == 0 {
		s.submissionMu.Unlock()
		return Group{}, &serviceError{409, errors.New("没有可恢复的超时回复")}
	}
	originalUpdated := group.UpdatedAt
	lastMessage := ""
	if len(messages) > 0 {
		lastMessage = messages[len(messages)-1].ID
	}
	s.submissionMu.Unlock()

	for i := range items {
		items[i].reply, err = controller.RecoverTimeout(ctx, group, items[i].member, items[i].user, items[i].failure)
		if err != nil {
			return Group{}, &serviceError{409, fmt.Errorf("%s：%w", memberName(items[i].member), err)}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, _ := store.ParseTime(items[i].reply.CreatedAt)
		right, _ := store.ParseTime(items[j].reply.CreatedAt)
		return left.Before(right)
	})

	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	current, err := s.get(ctx, id)
	if err != nil || current.UpdatedAt != originalUpdated {
		return Group{}, &serviceError{409, errors.New("群聊状态已变化，请重新恢复")}
	}
	latest, err := s.messages(ctx, id)
	if err != nil {
		return Group{}, err
	}
	currentLast := ""
	if len(latest) > 0 {
		currentLast = latest[len(latest)-1].ID
	}
	if currentLast != lastMessage {
		return Group{}, &serviceError{409, errors.New("群聊消息已变化，请重新恢复")}
	}
	var events []store.Event
	err = s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		g, err := readGroupTx(ctx, tx, id)
		if err != nil {
			return err
		}
		flow := ensureCoordination(&g, latest)
		for _, item := range items {
			mid := memberID(item.member)
			for _, existing := range latest {
				if existing.SessionID == item.failure.SessionID {
					for _, nativeID := range existing.NativeIDs {
						if nativeID == item.reply.NativeIDs[0] {
							return &serviceError{409, errors.New("该回复已在群聊中")}
						}
					}
				}
			}
			messageID, err := randomID("gmsg-")
			if err != nil {
				return err
			}
			message := Message{ID: messageID, GroupID: id, SenderType: "agent", SenderID: item.member.AgentID, SenderMemberID: mid,
				SenderName: memberName(item.member), Text: item.reply.Text, Mentions: Mentions(item.reply.Text, g.Members),
				HopCount: item.failure.HopCount, CreatedAt: item.reply.CreatedAt, SessionID: item.failure.SessionID, NativeIDs: item.reply.NativeIDs}
			messageEvents, _, err := appendMessageTx(ctx, tx, message)
			if err != nil {
				return err
			}
			events = append(events, messageEvents...)
			flow.Delivered[mid] = item.user.ID
			state := g.MemberStates[mid]
			state.Status, state.LastError, state.UpdatedAt = "success", "", now()
			g.MemberStates[mid] = state
			for _, target := range message.Mentions {
				if target == mid || g.MemberStates[target].Muted {
					continue
				}
				pending := flow.Pending[target]
				pending.Hop = max(pending.Hop, message.HopCount+1)
				flow.Pending[target] = pending
			}
		}
		for mid := range flow.Pending {
			state := g.MemberStates[mid]
			if state.Status != "running" && state.Status != "unknown" {
				state.Status, state.UpdatedAt = "waiting", now()
				g.MemberStates[mid] = state
			}
		}
		flow.Status, flow.Error = "deciding", ""
		flow.Revision++
		g.UpdatedAt = now()
		if err := putTx(ctx, tx, "bot_group:"+id, g); err != nil {
			return err
		}
		event, err := appendEventTx(ctx, tx, "bot_group.upsert", g)
		if err != nil {
			return err
		}
		events = append(events, event)
		current = g
		return nil
	})
	if err != nil {
		return Group{}, err
	}
	s.options.Publisher.PublishCommitted(events)
	s.schedule(id)
	return current, nil
}
