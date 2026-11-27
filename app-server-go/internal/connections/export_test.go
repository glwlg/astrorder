package connections

// SetAgyQuotaQueryPausedForTest overrides agyQuotaQueryPaused for a single test
// and returns a restore callback.
func SetAgyQuotaQueryPausedForTest(paused bool) func() {
	prev := agyQuotaQueryPaused
	agyQuotaQueryPaused = paused
	return func() {
		agyQuotaQueryPaused = prev
	}
}
