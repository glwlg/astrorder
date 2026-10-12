package modelgateway

import (
	"context"

	"astrorder.dev/app-server/internal/store"
)

// SyncJobForTest aliases the unexported syncJob so external test packages can
// assert persisted job fields.
type SyncJobForTest = syncJob

// ListJobsForTest exposes listJobs to external test packages.
func ListJobsForTest(ctx context.Context, st *store.Store) ([]SyncJobForTest, error) {
	return listJobs(ctx, st)
}
