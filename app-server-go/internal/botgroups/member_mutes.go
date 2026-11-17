package botgroups

import (
	"context"
	"encoding/json"
	"errors"
)

// Muting affects future delivery only. In-flight work can finish, but its
// delivery cursor must not rewind the history boundary recorded on restoration.
func (s *Service) updateMemberMutes(ctx context.Context, id string, raw json.RawMessage) (Group, error) {
	var changes map[string]*bool
	if err := json.Unmarshal(raw, &changes); err != nil || len(changes) == 0 {
		return Group{}, errors.New("无效的成员禁言设置")
	}
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	history, err := s.messages(ctx, id)
	if err != nil {
		return Group{}, err
	}
	through := ""
	if len(history) > 0 {
		through = history[len(history)-1].ID
	}
	group, err := s.mutate(ctx, id, func(group *Group) error {
		members := make(map[string]bool, len(group.Members))
		for _, member := range group.Members {
			members[memberID(member)] = true
		}
		for mid, muted := range changes {
			if !members[mid] || muted == nil {
				return errors.New("无效的成员禁言设置")
			}
		}
		if group.MemberStates == nil {
			group.MemberStates = make(map[string]MemberState)
		}
		flow := ensureCoordination(group, history)
		changed := false
		for mid, muted := range changes {
			state := group.MemberStates[mid]
			if state.Muted == *muted {
				continue
			}
			changed = true
			state.Muted, state.UpdatedAt = *muted, now()
			if state.Status == "waiting" {
				state.Status = "idle"
			}
			group.MemberStates[mid] = state
			delete(flow.Pending, mid)
			// A separate cursor survives late acceptance/completion of older
			// commands, including a failure that rolls back Delivered.
			flow.SkippedThrough[mid] = through
		}
		if changed {
			flow.Revision++
			if flow.Status != "stopped" {
				flow.Status, flow.Error = "deciding", ""
				if len(flow.Pending) == 0 && len(flow.InFlight) == 0 {
					flow.Status = "complete"
				}
				if detail := coordinationFailure(group, flow); detail != "" {
					flow.Status, flow.Error = "error", detail
				}
			}
		}
		return nil
	})
	if err == nil && group.Coordination.Status == "deciding" {
		s.schedule(id)
	}
	return group, err
}
