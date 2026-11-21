package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

const (
	quotaCacheTTL       = 60 * time.Second
	quotaStaleTTL       = 5 * time.Minute
	quotaErrorBackoff   = 15 * time.Second
	quotaCommandTimeout = 20 * time.Second
)

type cliUsageEnvelope struct {
	Status  string `json:"status"`
	Command struct {
		Name string `json:"name"`
		Data struct {
			Description string          `json:"description"`
			Groups      []cliUsageGroup `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

type cliUsageGroup struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Buckets     []cliUsageBucket `json:"buckets"`
}

type cliUsageBucket struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Window            string   `json:"window"`
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
}

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

func cloneTierMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	cp := make(map[string]any, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp
}

func cloneGroups(groups []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		gCopy := make(map[string]any, len(g))
		for k, v := range g {
			if k == "buckets" {
				if buckets, ok := v.([]map[string]any); ok {
					bCopy := make([]map[string]any, 0, len(buckets))
					for _, b := range buckets {
						bItem := make(map[string]any, len(b))
						for bk, bv := range b {
							bItem[bk] = bv
						}
						bCopy = append(bCopy, bItem)
					}
					gCopy["buckets"] = bCopy
					continue
				}
			}
			gCopy[k] = v
		}
		out = append(out, gCopy)
	}
	return out
}

func applyTierMetadata(out, tier map[string]any) {
	if tier == nil {
		return
	}
	plan := strings.TrimSpace(firstNonEmptyStr(tier, "name", "description"))
	tierID := strings.TrimSpace(str(tier, "id"))
	if plan != "" {
		out["plan"] = plan
		out["plan_name"] = plan
	}
	if tierID != "" {
		out["tier_id"] = tierID
	}
}

func buildTierOnlyQuotaResponse(tier map[string]any) map[string]any {
	out := map[string]any{
		"provider": "antigravity",
		"as_of":    time.Now().UTC().Format(time.RFC3339Nano),
		"models":   []map[string]any{},
	}
	applyTierMetadata(out, tier)
	return out
}

func buildCachedQuotaResponse(cache, tier map[string]any) map[string]any {
	out := map[string]any{
		"provider": "antigravity",
		"as_of":    cache["as_of"],
		"models":   []map[string]any{},
	}
	if source, ok := cache["source"].(string); ok && source != "" {
		out["source"] = source
	}
	if desc, ok := cache["description"].(string); ok && desc != "" {
		out["description"] = desc
	}
	if groups, ok := cache["groups"].([]map[string]any); ok {
		out["groups"] = cloneGroups(groups)
	}
	applyTierMetadata(out, tier)
	return out
}

func parseUsageCLIOutput(raw []byte) ([]map[string]any, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("agy /usage returned empty output")
	}
	var env cliUsageEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		idx := bytes.IndexByte(trimmed, '{')
		if idx <= 0 {
			return nil, "", fmt.Errorf("decode agy /usage JSON: %w", err)
		}
		if err2 := json.Unmarshal(trimmed[idx:], &env); err2 != nil {
			return nil, "", fmt.Errorf("decode agy /usage JSON: %w", err2)
		}
	}

	groups := make([]map[string]any, 0, len(env.Command.Data.Groups))
	for _, g := range env.Command.Data.Groups {
		buckets := make([]map[string]any, 0, len(g.Buckets))
		for _, b := range g.Buckets {
			rem := 0.0
			if b.RemainingFraction != nil {
				rem = *b.RemainingFraction
			}
			if math.IsNaN(rem) || math.IsInf(rem, 0) {
				rem = 0
			}
			if rem < 0 {
				rem = 0
			} else if rem > 1 {
				rem = 1
			}
			usedPercent := math.Round((1.0-rem)*1000) / 10
			if usedPercent < 0 {
				usedPercent = 0
			} else if usedPercent > 100 {
				usedPercent = 100
			}

			bucketMap := map[string]any{
				"id":                 b.ID,
				"name":               b.Name,
				"description":        b.Description,
				"window":             b.Window,
				"remaining_fraction": rem,
				"used_percent":       usedPercent,
			}
			if rt := strings.TrimSpace(b.ResetTime); rt != "" {
				parsed, err := time.Parse(time.RFC3339Nano, rt)
				if err != nil {
					parsed, err = time.Parse(time.RFC3339, rt)
				}
				// Ignore Unix epoch 0 sentinel ("1970-01-01T00:00:00Z") reported when full.
				if err == nil && parsed.Unix() > 86400 {
					bucketMap["reset_at"] = parsed.UnixMilli()
					bucketMap["reset_time"] = parsed.UTC().Format(time.RFC3339)
				}
			}
			buckets = append(buckets, bucketMap)
		}
		groups = append(groups, map[string]any{
			"name":        g.Name,
			"description": g.Description,
			"buckets":     buckets,
		})
	}
	return groups, strings.TrimSpace(env.Command.Data.Description), nil
}

func (a *Adapter) fetchUsageCLI(ctx context.Context) ([]map[string]any, string, error) {
	baseArgs := cleanACPArgs(a.config.Arguments)
	if len(baseArgs) > 0 && baseArgs[len(baseArgs)-1] == "acp" {
		baseArgs = baseArgs[:len(baseArgs)-1]
	}
	args := append(baseArgs, "-p", "/usage", "--output-format", "json")

	if a.sshStarter != nil {
		dir, err := a.workspace(a.config.Workspace)
		if err != nil {
			return nil, "", err
		}
		sess, err := a.sshStarter(ctx, ssh.CommandSpec{
			Executable: a.config.Executable,
			Args:       args,
			Dir:        dir,
		})
		if err != nil {
			return nil, "", fmt.Errorf("Antigravity remote /usage failed: %w", err)
		}
		defer sess.Close()
		_ = sess.Stdin().Close()
		outBytes, readErr := io.ReadAll(sess.Stdout())
		exitStatus, waitErr := sess.Wait()
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if waitErr != nil || readErr != nil || !exitStatus.ConfirmedRemoteExit || exitStatus.ExitCode != 0 {
			if waitErr != nil {
				return nil, "", fmt.Errorf("Antigravity remote /usage failed: %w", waitErr)
			}
			return nil, "", fmt.Errorf("Antigravity remote /usage failed (exit %d)", exitStatus.ExitCode)
		}
		return parseUsageCLIOutput(outBytes)
	}

	if strings.TrimSpace(a.config.Executable) == "" {
		return nil, "", fmt.Errorf("Antigravity executable is not configured")
	}
	cmd := exec.Command(a.config.Executable, args...)
	cmd.Env = process.NativeEnvironment(a.config.Environment)
	if strings.TrimSpace(a.config.Workspace) != "" {
		cmd.Dir = a.config.Workspace
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	owner, err := process.StartOwnedCommand(cmd)
	if err != nil {
		return nil, "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer owner.Close()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = owner.Close()
		<-done
		return nil, "", ctx.Err()
	}
	if err != nil {
		return nil, "", fmt.Errorf("Antigravity /usage failed: %w", err)
	}
	return parseUsageCLIOutput(out.Bytes())
}

// quotaRead returns subscription quota groups from the official `agy -p '/usage'
// --output-format json` command with singleflight + 60s caching, enriched with
// subscription tier metadata from agy acp (_meta.antigravity.tier).
func (a *Adapter) quotaRead(ctx context.Context) (map[string]any, error) {
	for {
		a.quotaMu.Lock()
		tier := cloneTierMap(a.tierInfo)
		if a.quotaCache != nil && time.Since(a.quotaCachedAt) < quotaCacheTTL {
			out := buildCachedQuotaResponse(a.quotaCache, tier)
			a.quotaMu.Unlock()
			return out, nil
		}
		if a.quotaInFlight != nil {
			wait := a.quotaInFlight
			a.quotaMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if !a.quotaFailedAt.IsZero() && time.Since(a.quotaFailedAt) < quotaErrorBackoff {
			if a.quotaCache != nil && time.Since(a.quotaCachedAt) < quotaStaleTTL {
				out := buildCachedQuotaResponse(a.quotaCache, tier)
				a.quotaMu.Unlock()
				return out, nil
			}
			a.quotaMu.Unlock()
			return buildTierOnlyQuotaResponse(tier), nil
		}
		wait := make(chan struct{})
		a.quotaInFlight = wait
		a.quotaMu.Unlock()

		fetchCtx, cancel := context.WithTimeout(context.Background(), quotaCommandTimeout)
		groups, desc, err := a.fetchUsageCLI(fetchCtx)
		cancel()

		a.quotaMu.Lock()
		a.quotaInFlight = nil
		close(wait)
		tierLatest := cloneTierMap(a.tierInfo)
		if err == nil && len(groups) > 0 {
			entry := map[string]any{
				"source":      "agy_cli_usage",
				"as_of":       time.Now().UTC().Format(time.RFC3339Nano),
				"description": desc,
				"groups":      groups,
				"models":      []map[string]any{},
			}
			a.quotaCache = entry
			a.quotaCachedAt = time.Now()
			a.quotaFailedAt = time.Time{}
			out := buildCachedQuotaResponse(entry, tierLatest)
			a.quotaMu.Unlock()
			return out, nil
		}
		a.quotaFailedAt = time.Now()
		if a.quotaCache != nil && time.Since(a.quotaCachedAt) < quotaStaleTTL {
			out := buildCachedQuotaResponse(a.quotaCache, tierLatest)
			a.quotaMu.Unlock()
			return out, nil
		}
		a.quotaMu.Unlock()
		return buildTierOnlyQuotaResponse(tierLatest), nil
	}
}
