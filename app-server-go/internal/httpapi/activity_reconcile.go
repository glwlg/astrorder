package httpapi

import (
	"context"
	"log/slog"
	"time"
)

type activityRefresher interface {
	RefreshActivity(ctx context.Context, agentID string) (map[string]any, error)
}

const (
	startupReconcileAttempts = 3
	startupReconcileInterval = 10 * time.Second
)

// reconcileActivityOnStart repairs last-activity times missed while the App was
// down. It is the only automatic reconciliation: one batch query per Agent, and
// a retry only while some Agent still errors (its runtime may not have finished
// connecting yet). Afterwards activity arrives by event, or by manual refresh.
func reconcileActivityOnStart(refresher activityRefresher, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	for attempt := 1; attempt <= startupReconcileAttempts; attempt++ {
		time.Sleep(startupReconcileInterval)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		result, err := refresher.RefreshActivity(ctx, "")
		cancel()
		if err != nil {
			logger.Warn("startup activity reconcile failed", "attempt", attempt, "error", err)
			continue
		}
		failed := false
		if agents, ok := result["agents"].([]map[string]any); ok {
			for _, agent := range agents {
				if message, bad := agent["error"]; bad {
					failed = true
					logger.Warn("startup activity reconcile: agent unavailable", "attempt", attempt, "agent_id", agent["agent_id"], "error", message)
				}
			}
		}
		logger.Info("startup activity reconcile", "attempt", attempt, "updated", result["updated"])
		if !failed {
			return
		}
	}
}
