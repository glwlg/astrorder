package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astrorder.dev/app-server/internal/learning"
)

func bindLearningCallbacks(agentHandler any, svc *learning.Service) {
	if svc == nil {
		return
	}
	binder, ok := agentHandler.(interface {
		SetLearningCallbacks(
			func(context.Context, map[string]any) (any, error),
			func(context.Context, map[string]any) (any, error),
			func(context.Context, map[string]any) (any, error),
			func(context.Context, map[string]any) (any, error),
		)
	})
	if !ok {
		return
	}
	binder.SetLearningCallbacks(
		func(ctx context.Context, payload map[string]any) (any, error) {
			return svc.StatusForAgent(ctx, payloadString(payload, "status"))
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var req learning.IngestRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, fmt.Errorf("解析 learning.ingest 参数失败: %w", err)
			}
			return svc.IngestFromAgent(ctx, req)
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			topicID := payloadString(payload, "topic_id")
			if topicID == "" {
				topicID = payloadString(payload, "id")
			}
			if topicID == "" {
				return nil, fmt.Errorf("topic_id is required")
			}
			req := learning.UpdateTopicRequest{}
			if s := payloadString(payload, "status"); s != "" {
				req.Status = &s
			}
			if n, ok := payload["study_notes"].(string); ok {
				req.StudyNotes = &n
			} else if n, ok := payload["notes"].(string); ok {
				req.StudyNotes = &n
			}
			if p, ok := payload["notes_file_path"].(string); ok {
				req.NotesFilePath = &p
			} else if p, ok := payload["notes_file"].(string); ok {
				req.NotesFilePath = &p
			}
			if d := int(payloadInt64(payload, "snooze_days")); d > 0 {
				req.SnoozeDays = &d
			}
			return svc.UpdateFromAgent(ctx, topicID, req)
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			days := int(payloadInt64(payload, "lookback_days"))
			if days <= 0 {
				days = int(payloadInt64(payload, "days"))
			}
			botID := payloadString(payload, "bot_id")
			if botID == "" {
				botID = payloadString(payload, "bot")
			}
			return svc.DispatchScan(ctx, learning.TriggerScanRequest{
				LookbackDays: days,
				BotID:        strings.TrimSpace(botID),
				Trigger:      "agent",
			})
		},
	)
}
