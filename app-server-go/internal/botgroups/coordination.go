package botgroups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"astrorder.dev/app-server/internal/store"
)

// Coordination persists delivery cursors independently for each group member.
// Message contents remain in the existing group/native histories.
type Coordination struct {
	Status         string                     `json:"status"`
	Error          string                     `json:"error,omitempty"`
	Revision       uint64                     `json:"revision"`
	Pending        map[string]PendingDelivery `json:"pending,omitempty"`
	Delivered      map[string]string          `json:"delivered,omitempty"`
	SkippedThrough map[string]string          `json:"skipped_through,omitempty"`
	InFlight       map[string]Delivery        `json:"in_flight,omitempty"`
}

type PendingDelivery struct {
	Hop int `json:"hop"`
}

type Delivery struct {
	CommandID        string   `json:"command_id"`
	PreviousID       string   `json:"previous_id,omitempty"`
	ThroughID        string   `json:"through_id"`
	Hop              int      `json:"hop"`
	BaselineCaptured bool     `json:"baseline_captured,omitempty"`
	BaselineIDs      []string `json:"baseline_ids,omitempty"`
}

var errStaleDecision = errors.New("group decision became stale")

type recoveryController interface {
	Resume(context.Context, Turn, []string) (Reply, error)
}

func (s *Service) resume(id string, member Member, session string, delivery Delivery) {
	controller, ok := s.options.Controller.(recoveryController)
	if !ok {
		return
	}
	mid := memberID(member)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.active[id] == nil {
		s.active[id] = make(map[string]*activeTurn)
	}
	if s.active[id][mid] != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	run := &activeTurn{turn: Turn{GroupID: id, Member: member, SessionID: session, CommandID: delivery.CommandID, ThroughID: delivery.ThroughID, Hop: delivery.Hop}, cancel: cancel}
	s.active[id][mid] = run
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		turn := run.turn
		turn.RecordAccepted = func() error { return s.recordAccepted(ctx, id, mid, delivery.CommandID) }
		reply, err := controller.Resume(ctx, turn, delivery.BaselineIDs)
		s.finishDelivery(id, member, run, reply, err)
	}()
}

func (s *Service) recordAccepted(ctx context.Context, id, mid, commandID string) error {
	_, err := s.mutate(ctx, id, func(g *Group) error {
		if g.Coordination == nil {
			return errors.New("群聊任务状态不存在")
		}
		flow := ensureCoordination(g, nil)
		delivery, ok := flow.InFlight[mid]
		if !ok || delivery.CommandID != commandID {
			return errors.New("群聊任务已变化")
		}
		flow.Delivered[mid] = delivery.ThroughID
		flow.Revision++
		return nil
	})
	if err == nil {
		s.schedule(id)
	}
	return err
}

func ensureCoordination(group *Group, prior []Message) *Coordination {
	if group.Coordination == nil {
		group.Coordination = &Coordination{Status: "idle"}
		// Existing groups predate delivery cursors. Their earlier messages may
		// already have been delivered, so do not replay them as new work.
		if len(prior) > 0 {
			group.Coordination.Delivered = make(map[string]string, len(group.Members))
			for _, member := range group.Members {
				group.Coordination.Delivered[memberID(member)] = prior[len(prior)-1].ID
			}
		}
	}
	flow := group.Coordination
	if flow.Pending == nil {
		flow.Pending = make(map[string]PendingDelivery)
	}
	if flow.Delivered == nil {
		flow.Delivered = make(map[string]string)
	}
	if flow.SkippedThrough == nil {
		flow.SkippedThrough = make(map[string]string)
	}
	if flow.InFlight == nil {
		flow.InFlight = make(map[string]Delivery)
	}
	return flow
}

func unreadMessages(messages []Message, after, skippedThrough, member string) ([]Message, error) {
	start := 0
	for _, cursor := range []string{after, skippedThrough} {
		if cursor == "" {
			continue
		}
		found := false
		for i, message := range messages {
			if message.ID == cursor {
				start = max(start, i+1)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("群聊消息位置不可用，请检查会话记录")
		}
	}
	unread := make([]Message, 0, len(messages)-start)
	for _, message := range messages[start:] {
		if message.SenderType == "agent" && message.SenderMemberID == member {
			continue
		}
		unread = append(unread, message)
	}
	return unread, nil
}

func memberName(member Member) string {
	if member.Alias != nil && strings.TrimSpace(*member.Alias) != "" {
		return strings.TrimSpace(*member.Alias)
	}
	if member.Name != nil && strings.TrimSpace(*member.Name) != "" {
		return strings.TrimSpace(*member.Name)
	}
	return member.AgentID
}

func coordinationFailure(group *Group, flow *Coordination) string {
	for _, member := range group.Members {
		mid := memberID(member)
		state := group.MemberStates[mid]
		if _, pending := flow.Pending[mid]; pending && state.Status == "failed" {
			return memberName(member) + " 执行失败，请核对会话"
		}
		if _, running := flow.InFlight[mid]; running && state.Status == "unknown" {
			return "成员的执行结果尚未确认，请核对会话"
		}
	}
	return ""
}

// queueUserMessage stores the message and its recipients in one transaction.
// The caller holds submissionMu so stop, delivery, and new messages are ordered.
func (s *Service) queueUserMessage(ctx context.Context, groupID string, message Message, targets []string, prior []Message) (int, error) {
	var events []store.Event
	var count int
	err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		group, err := readGroupTx(ctx, tx, groupID)
		if err != nil {
			return err
		}
		flow := ensureCoordination(&group, prior)
		for _, target := range targets {
			state := group.MemberStates[target]
			if state.Muted {
				continue
			}
			if state.Status == "unknown" {
				return &serviceError{409, errors.New("成员仍有未确认的执行结果，请先核对")}
			}
			flow.Pending[target] = PendingDelivery{Hop: 1}
			if state.Status != "running" && state.Status != "failed" {
				state.Status = "waiting"
				state.LastError = ""
				state.UpdatedAt = now()
				group.MemberStates[target] = state
			}
		}
		flow.Status = "deciding"
		flow.Error = ""
		if len(flow.Pending) == 0 && len(flow.InFlight) == 0 {
			flow.Status = "complete"
		}
		if detail := coordinationFailure(&group, flow); detail != "" {
			flow.Status, flow.Error = "error", detail
		}
		flow.Revision++
		messageEvents, size, err := appendMessageTx(ctx, tx, message)
		if err != nil {
			return err
		}
		count = size
		group.UpdatedAt = now()
		if err := putTx(ctx, tx, "bot_group:"+groupID, group); err != nil {
			return err
		}
		groupEvent, err := appendEventTx(ctx, tx, "bot_group.upsert", group)
		if err != nil {
			return err
		}
		events = append(messageEvents, groupEvent)
		return nil
	})
	if err == nil {
		s.options.Publisher.PublishCommitted(events)
	}
	return count, err
}

func (s *Service) schedule(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.scheduling[id] {
		s.reschedule[id] = true
		return
	}
	s.scheduling[id] = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			s.evaluate(id)
			s.mu.Lock()
			if s.reschedule[id] && !s.closed {
				delete(s.reschedule, id)
				s.mu.Unlock()
				continue
			}
			delete(s.reschedule, id)
			delete(s.scheduling, id)
			s.mu.Unlock()
			return
		}
	}()
}

func (s *Service) evaluate(id string) {
	s.submissionMu.Lock()
	group, err := s.get(s.ctx, id)
	if err != nil || group.Coordination == nil || group.Coordination.Status == "stopped" || group.Coordination.Status == "error" || group.Coordination.Status == "complete" {
		s.submissionMu.Unlock()
		return
	}
	messages, err := s.messages(s.ctx, id)
	if err != nil {
		revision := group.Coordination.Revision
		s.submissionMu.Unlock()
		s.decisionError(id, revision, "读取群聊记录失败："+err.Error())
		return
	}
	flow := group.Coordination
	currentHop := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].SenderType == "user" {
			break
		}
		currentHop = max(currentHop, messages[i].HopCount)
	}
	for _, delivery := range flow.InFlight {
		currentHop = max(currentHop, delivery.Hop)
	}
	input := DecisionInput{
		Group: DecisionGroup{
			Name: group.Name, Description: group.Description, Active: append([]string{}, group.ActiveSpeakers...),
			CurrentHop: currentHop, MaxHops: group.MaxHops, Remaining: max(0, group.MaxHops-currentHop),
		},
		Messages:   decisionMessages(messages),
		Candidates: make([]DecisionCandidate, 0, len(flow.Pending)),
	}
	for _, member := range group.Members {
		mid := memberID(member)
		if group.MemberStates[mid].Muted {
			input.Group.Muted = append(input.Group.Muted, mid)
			continue
		}
		pending, ok := flow.Pending[mid]
		if !ok {
			continue
		}
		unread, readErr := unreadMessages(messages, flow.Delivered[mid], flow.SkippedThrough[mid], mid)
		if readErr != nil {
			revision := flow.Revision
			s.submissionMu.Unlock()
			s.decisionError(id, revision, readErr.Error())
			return
		}
		input.Candidates = append(input.Candidates, DecisionCandidate{
			MemberID: mid, Name: memberName(member), Role: memberValue(member.SystemRolePrompt),
			Hop:    pending.Hop,
			Busy:   group.MemberStates[mid].Status == "running" || group.MemberStates[mid].Status == "unknown" || group.MemberStates[mid].Status == "failed",
			Unread: decisionMessages(unread), Messages: unread,
		})
	}
	revision := flow.Revision
	lastID := ""
	if len(messages) > 0 {
		lastID = messages[len(messages)-1].ID
	}
	if len(input.Candidates) == 0 && len(flow.InFlight) > 0 {
		s.submissionMu.Unlock()
		return
	}
	s.submissionMu.Unlock()

	decision, err := s.options.Decider.Decide(s.ctx, input)
	if err != nil {
		s.decisionError(id, revision, err.Error())
		return
	}
	for _, candidate := range input.Candidates {
		if _, ok := decision.Send[candidate.MemberID]; !ok {
			s.decisionError(id, revision, "System One 未返回完整的成员判断")
			return
		}
	}

	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	current, err := s.get(s.ctx, id)
	if err != nil || current.Coordination == nil || current.Coordination.Revision != revision {
		return
	}
	latest, err := s.messages(s.ctx, id)
	if err != nil {
		s.decisionErrorLocked(id, revision, "读取群聊记录失败："+err.Error())
		return
	}
	currentLast := ""
	if len(latest) > 0 {
		currentLast = latest[len(latest)-1].ID
	}
	if currentLast != lastID {
		s.schedule(id)
		return
	}

	type launch struct {
		member      Member
		prompt      string
		hop         int
		through     string
		command     string
		attachments []map[string]any
	}
	launches := []launch{}
	members := make(map[string]Member, len(current.Members))
	for _, member := range current.Members {
		members[memberID(member)] = member
	}
	for _, candidate := range input.Candidates {
		pending := current.Coordination.Pending[candidate.MemberID]
		if current.MemberStates[candidate.MemberID].Muted || !decision.Send[candidate.MemberID] || candidate.Busy || len(candidate.Messages) == 0 {
			continue
		}
		if pending.Hop > current.MaxHops {
			s.decisionErrorLocked(id, revision, fmt.Sprintf("下一次接力为第 %d 次，超过本轮上限 %d 次，消息尚未发送。请在群设置中提高上限后重新判断，或中断本轮。", pending.Hop, current.MaxHops))
			return
		}
		commandID, idErr := randomID("cmd-g-")
		if idErr != nil {
			s.decisionErrorLocked(id, revision, "无法创建群聊任务")
			return
		}
		launches = append(launches, launch{members[candidate.MemberID], deliveryPrompt(current, members[candidate.MemberID], pending.Hop, candidate.Messages), pending.Hop, lastID, commandID, deliveryAttachments(candidate.Messages)})
	}
	updated, err := s.mutate(s.ctx, id, func(g *Group) error {
		flow := g.Coordination
		if flow == nil || flow.Revision != revision {
			return errors.New("群聊状态已变化")
		}
		flow = ensureCoordination(g, nil)
		for _, item := range launches {
			mid := memberID(item.member)
			delete(flow.Pending, mid)
			flow.InFlight[mid] = Delivery{CommandID: item.command, PreviousID: flow.Delivered[mid], ThroughID: item.through, Hop: item.hop}
			state := g.MemberStates[mid]
			state.Status, state.LastError, state.UpdatedAt = "running", "", now()
			g.MemberStates[mid] = state
			g.ActiveSpeakers = append(g.ActiveSpeakers, mid)
			g.ActiveHop = max(g.ActiveHop, item.hop)
		}
		if len(g.ActiveSpeakers) > 0 {
			g.ActiveSpeaker = &g.ActiveSpeakers[0]
			flow.Status = "running"
		} else if decision.Complete {
			flow.Status = "complete"
			for mid := range flow.Pending {
				state := g.MemberStates[mid]
				if state.Status == "waiting" {
					state.Status, state.UpdatedAt = "idle", now()
					g.MemberStates[mid] = state
				}
			}
			flow.Pending = make(map[string]PendingDelivery)
			g.ActiveHop = 0
		} else {
			flow.Status = "waiting"
			g.ActiveHop = 0
		}
		flow.Error = ""
		flow.Revision++
		return nil
	})
	if err != nil {
		slog.Warn("group decision could not be saved", "error", err)
		return
	}
	for _, item := range launches {
		s.dispatch(updated.ID, item.member, item.prompt, item.hop, item.through, item.command, item.attachments)
	}
}

func decisionMessages(messages []Message) []DecisionMessage {
	result := make([]DecisionMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, DecisionMessage{SenderType: message.SenderType, SenderID: message.SenderMemberID, SenderName: message.SenderName, Text: message.Text, HopCount: message.HopCount, ImageCount: len(message.Attachments)})
	}
	return result
}

func deliveryPrompt(group Group, member Member, hop int, messages []Message) string {
	identity := []string{
		fmt.Sprintf("你正在参与星序多 Agent 协作群「%s」。", group.Name),
		fmt.Sprintf("你在本群的身份是 @%s。请以这个身份回复，不要代替其他成员发言。", memberName(member)),
		fmt.Sprintf("本次是本轮第 %d 次接力，上限 %d 次；本次回复后剩余 %d 次。请据此安排交接。", hop, group.MaxHops, max(0, group.MaxHops-hop)),
	}
	if member.SystemRolePrompt != nil && strings.TrimSpace(*member.SystemRolePrompt) != "" {
		identity = append(identity, "你的群内职责："+strings.TrimSpace(*member.SystemRolePrompt))
	}
	if workspace := memberValue(member.Workspace); workspace != "" {
		identity = append(identity, "你的工作空间："+workspace)
	}
	sections := []string{
		strings.Join(identity, "\n"),
		"请结合以下尚未发给你的群消息处理当前任务；区分发给你的任务与其他成员的任务，需要交接时明确 @对应成员名称。",
		fmt.Sprintf("共享黑板 session_key=group::%s", group.ID),
	}
	roster := []string{"群成员："}
	for _, candidate := range group.Members {
		label := "- @" + memberName(candidate)
		if memberID(candidate) == memberID(member) {
			label += "（你）"
		}
		roster = append(roster, label)
	}
	sections = append(sections, strings.Join(roster, "\n"), "以下尚未发送给你的消息按群聊顺序排列：")
	for _, message := range messages {
		sections = append(sections, "["+message.SenderName+"]\n"+message.Text)
		if len(message.Attachments) > 0 {
			names := make([]string, 0, len(message.Attachments))
			for _, attachment := range message.Attachments {
				names = append(names, fmt.Sprint(attachment["name"]))
			}
			sections = append(sections, "随消息附带图片："+strings.Join(names, "、"))
		}
	}
	return strings.Join(sections, "\n\n")
}

func (s *Service) finishDelivery(id string, member Member, run *activeTurn, reply Reply, runErr error) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	mid := memberID(member)
	s.mu.Lock()
	current := s.active[id][mid] == run
	sessionID := run.turn.SessionID
	s.mu.Unlock()
	if !current || s.ctx.Err() != nil {
		return
	}
	messageID, err := randomID("gmsg-")
	if err != nil {
		slog.Warn("group reply could not be identified", "error", err)
		return
	}
	var events []store.Event
	err = s.options.Store.WithTx(s.ctx, func(tx *sql.Tx) error {
		group, err := readGroupTx(s.ctx, tx, id)
		if err != nil {
			return err
		}
		if group.Coordination == nil {
			return errors.New("群聊任务状态不存在")
		}
		flow := ensureCoordination(&group, nil)
		delivery, ok := flow.InFlight[mid]
		if !ok || delivery.CommandID != run.turn.CommandID {
			return errors.New("群聊任务已变化")
		}
		state := group.MemberStates[mid]
		state.Status, state.LastError, state.UpdatedAt = "success", "", now()
		if sessionID != "" {
			state.SessionID = sessionID
		}
		if runErr != nil {
			state.Status, state.LastError = "failed", runErr.Error()
			var uncertain *uncertainDeliveryError
			if errors.As(runErr, &uncertain) {
				state.Status = "unknown"
			} else {
				delete(flow.InFlight, mid)
				if flow.Delivered[mid] == delivery.ThroughID {
					flow.Delivered[mid] = delivery.PreviousID
				}
				if !state.Muted {
					pending := flow.Pending[mid]
					pending.Hop = max(pending.Hop, delivery.Hop)
					flow.Pending[mid] = pending
				}
			}
			reply.Text = "执行失败：" + runErr.Error()
		} else {
			delete(flow.InFlight, mid)
			flow.Delivered[mid] = delivery.ThroughID
			if _, waiting := flow.Pending[mid]; waiting {
				state.Status = "waiting"
			}
		}
		group.MemberStates[mid] = state
		message := Message{
			ID: messageID, GroupID: id, SenderType: "agent", SenderID: member.AgentID,
			SenderMemberID: mid, SenderName: memberName(member), Text: reply.Text,
			Mentions: Mentions(reply.Text, group.Members), HopCount: delivery.Hop,
			CreatedAt: now(), SessionID: sessionID, NativeIDs: reply.NativeIDs,
		}
		if runErr != nil {
			message.SenderType = "system"
		}
		if message.Text != "" {
			messageEvents, _, err := appendMessageTx(s.ctx, tx, message)
			if err != nil {
				return err
			}
			events = append(events, messageEvents...)
		}
		if runErr == nil {
			for _, target := range message.Mentions {
				if target == mid || group.MemberStates[target].Muted {
					continue
				}
				pending := flow.Pending[target]
				pending.Hop = max(pending.Hop, delivery.Hop+1)
				flow.Pending[target] = pending
				targetState := group.MemberStates[target]
				if targetState.Status != "running" && targetState.Status != "unknown" && targetState.Status != "failed" {
					targetState.Status, targetState.LastError, targetState.UpdatedAt = "waiting", "", now()
					group.MemberStates[target] = targetState
				}
			}
		}
		remaining := make([]string, 0, len(group.ActiveSpeakers))
		for _, speaker := range group.ActiveSpeakers {
			if speaker != mid {
				remaining = append(remaining, speaker)
			}
		}
		group.ActiveSpeakers = remaining
		group.ActiveSpeaker = nil
		if len(remaining) > 0 {
			group.ActiveSpeaker = &remaining[0]
		} else {
			group.ActiveHop = 0
		}
		flow.Status, flow.Error = "deciding", ""
		if detail := coordinationFailure(&group, flow); detail != "" {
			flow.Status, flow.Error = "error", detail
		}
		flow.Revision++
		group.UpdatedAt = now()
		if err := putTx(s.ctx, tx, "bot_group:"+id, group); err != nil {
			return err
		}
		event, err := appendEventTx(s.ctx, tx, "bot_group.upsert", group)
		if err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		slog.Warn("group reply could not be saved", "error", err)
		return
	}
	s.options.Publisher.PublishCommitted(events)
	s.mu.Lock()
	if s.active[id][mid] == run {
		delete(s.active[id], mid)
	}
	s.mu.Unlock()
	s.schedule(id)
	s.scheduleArtifacts(id)
}

func (s *Service) decisionError(id string, revision uint64, detail string) {
	if s.ctx.Err() != nil {
		return
	}
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	s.decisionErrorLocked(id, revision, detail)
}

func (s *Service) decisionErrorLocked(id string, revision uint64, detail string) {
	_, err := s.mutate(s.ctx, id, func(g *Group) error {
		if g.Coordination == nil || g.Coordination.Revision != revision {
			return errStaleDecision
		}
		g.Coordination.Status = "error"
		g.Coordination.Error = detail
		g.Coordination.Revision++
		return nil
	})
	if err != nil && !errors.Is(err, errStaleDecision) && s.ctx.Err() == nil {
		slog.Warn("group decision error could not be saved", "error", err)
	}
}

func (s *Service) retryDecision(ctx context.Context, id string) (Group, error) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	type recovery struct {
		member   Member
		session  string
		delivery Delivery
	}
	recoveries := []recovery{}
	group, err := s.mutate(ctx, id, func(g *Group) error {
		if g.Coordination == nil || g.Coordination.Status != "error" {
			return &serviceError{409, errors.New("群聊当前没有需要重试的判断")}
		}
		_, canResume := s.options.Controller.(recoveryController)
		for _, member := range g.Members {
			mid := memberID(member)
			state := g.MemberStates[mid]
			if _, pending := g.Coordination.Pending[mid]; pending && state.Status == "failed" {
				state.Status, state.LastError, state.UpdatedAt = "waiting", "", now()
				g.MemberStates[mid] = state
				continue
			}
			if state.Status != "unknown" {
				continue
			}
			delivery, ok := g.Coordination.InFlight[mid]
			if !canResume || !ok || state.SessionID == "" || delivery.CommandID == "" || !delivery.BaselineCaptured {
				return &serviceError{409, errors.New("成员执行结果无法自动核对，请先检查会话")}
			}
			state.Status, state.LastError, state.UpdatedAt = "running", "", now()
			g.MemberStates[mid] = state
			g.ActiveSpeakers = append(g.ActiveSpeakers, mid)
			g.ActiveHop = max(g.ActiveHop, delivery.Hop)
			recoveries = append(recoveries, recovery{member, state.SessionID, delivery})
		}
		if len(g.ActiveSpeakers) > 0 {
			g.ActiveSpeaker = &g.ActiveSpeakers[0]
		}
		g.Coordination.Status = "deciding"
		g.Coordination.Error = ""
		g.Coordination.Revision++
		return nil
	})
	if err == nil {
		for _, item := range recoveries {
			s.resume(id, item.member, item.session, item.delivery)
		}
		s.schedule(id)
	}
	return group, err
}
