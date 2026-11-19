package antigravity

import (
	"context"
	"strings"
	"time"
)

func (a *Adapter) setTierInfo(tier map[string]any) {
	if len(tier) == 0 {
		return
	}
	cp := make(map[string]any, len(tier))
	for k, v := range tier {
		cp[k] = v
	}
	a.quotaMu.Lock()
	a.tierInfo = cp
	a.quotaMu.Unlock()
}

// quotaRead returns compliant subscription tier metadata supplied by official
// agy acp (_meta.antigravity.tier) without probing internal LanguageServerService
// loopback endpoints or reading credential files.
func (a *Adapter) quotaRead(_ context.Context) (map[string]any, error) {
	a.quotaMu.Lock()
	var tier map[string]any
	if a.tierInfo != nil {
		tier = make(map[string]any, len(a.tierInfo))
		for k, v := range a.tierInfo {
			tier[k] = v
		}
	}
	a.quotaMu.Unlock()

	plan := ""
	tierID := ""
	if tier != nil {
		plan = strings.TrimSpace(firstNonEmptyStr(tier, "name", "description"))
		tierID = strings.TrimSpace(str(tier, "id"))
	}

	out := map[string]any{
		"provider": "antigravity",
		"as_of":    time.Now().UTC().Format(time.RFC3339Nano),
		"models":   []map[string]any{},
	}
	if plan != "" {
		out["plan"] = plan
		out["plan_name"] = plan
	}
	if tierID != "" {
		out["tier_id"] = tierID
	}
	return out, nil
}
