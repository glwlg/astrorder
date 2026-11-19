package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readDefaultModelForHome(home string) string {
	if home == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		return ""
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(str(cfg, "model"))
}

func defaultContextWindowForModel(model string) int64 {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "claude") || strings.Contains(lower, "gpt") || strings.Contains(lower, "sonnet") || strings.Contains(lower, "opus") {
		return 200000
	}
	if lower != "" {
		return 512000
	}
	return 512000
}

func openConversationDBForHome(home, sessionID string, readonly bool) (*sql.DB, bool, error) {
	if !validID(sessionID) {
		return nil, false, fmt.Errorf("invalid Antigravity conversation ID")
	}
	cliPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", sessionID+".db")
	desktopPath := filepath.Join(home, ".gemini", "antigravity", "conversations", sessionID+".db")
	if readonly {
		if db, err := openNative(desktopPath); err == nil {
			return db, true, nil
		}
		if db, err := openNative(cliPath); err == nil {
			return db, false, nil
		}
		return nil, true, os.ErrNotExist
	}
	if db, err := openNative(cliPath); err == nil {
		return db, false, nil
	}
	if db, err := openNative(desktopPath); err == nil {
		return db, true, nil
	}
	return nil, false, os.ErrNotExist
}

type usageStats struct {
	input      int64
	output     int64
	cacheWrite int64
	cacheRead  int64
	thinking   int64
}

func decodeModelUsageStats(b []byte) (usageStats, bool) {
	if len(b) == 0 {
		return usageStats{}, false
	}
	_, nums, err := wireFields(b)
	if err != nil {
		return usageStats{}, false
	}
	stats := usageStats{
		input:      int64(nums[2]),
		output:     int64(nums[3]),
		cacheWrite: int64(nums[4]),
		cacheRead:  int64(nums[5]),
		thinking:   int64(nums[10]),
	}
	if stats.input == 0 && stats.output == 0 && stats.cacheWrite == 0 && stats.cacheRead == 0 && stats.thinking == 0 {
		return usageStats{}, false
	}
	return stats, true
}

func readNativeSessionUsage(ctx context.Context, home, sessionID string, readonly bool) (map[string]any, error) {
	defaultModel := readDefaultModelForHome(home)
	db, _, err := openConversationDBForHome(home, sessionID, readonly)
	if err != nil {
		if os.IsNotExist(err) {
			out := map[string]any{
				"context_available": false,
				"provider":          "antigravity",
			}
			if defaultModel != "" {
				out["default_model"] = defaultModel
				out["model"] = defaultModel
			}
			return out, nil
		}
		return nil, err
	}
	defer db.Close()

	var (
		responseModel   string
		displayModel    string
		lastInputTokens int64
		maxWindow       int64
	)

	var genBlob []byte
	genErr := db.QueryRowContext(ctx, `SELECT data FROM gen_metadata ORDER BY idx DESC LIMIT 1`).Scan(&genBlob)
	if genErr == nil && len(genBlob) > 0 {
		if fields, _, e := wireFields(genBlob); e == nil {
			if cmRaw := field(fields, 1); len(cmRaw) > 0 {
				if cm, _, e := wireFields(cmRaw); e == nil {
					responseModel = strings.TrimSpace(string(field(cm, 19)))
					displayModel = strings.TrimSpace(string(field(cm, 21)))
					if stats, ok := decodeModelUsageStats(field(cm, 4)); ok {
						promptTotal := stats.input + stats.cacheRead + stats.cacheWrite
						if promptTotal > 0 {
							lastInputTokens = promptTotal
						}
					}
					if csRaw := field(cm, 9); len(csRaw) > 0 {
						if cs, _, e := wireFields(csRaw); e == nil {
							if cwmRaw := field(cs, 10); len(cwmRaw) > 0 {
								if _, cwmNums, e := wireFields(cwmRaw); e == nil {
									estUsed := int64(cwmNums[1])
									if w := int64(cwmNums[4]); w > 0 {
										maxWindow = w
									}
									if lastInputTokens == 0 && estUsed > 0 {
										lastInputTokens = estUsed
									}
								}
							}
						}
					}
				}
			}
			if responseModel == "" {
				if meta3Raw := field(fields, 3); len(meta3Raw) > 0 {
					if m3, _, e := wireFields(meta3Raw); e == nil {
						if gm := strings.TrimSpace(string(field(m3, 28))); gm != "" {
							responseModel = gm
						}
					}
				}
			}
		}
	}

	var (
		cumInput       int64
		cumOutput      int64
		cumCached      int64
		cumReasoning   int64
		lastStepPrompt int64
	)

	rows, qErr := db.QueryContext(ctx, `SELECT metadata, CASE WHEN length(metadata) > 0 THEN NULL ELSE step_payload END FROM steps ORDER BY idx ASC`)
	if qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var metaBlob, payloadBlob []byte
			if scanErr := rows.Scan(&metaBlob, &payloadBlob); scanErr != nil {
				continue
			}
			if len(metaBlob) == 0 && len(payloadBlob) > 0 {
				if pf, _, e := wireFields(payloadBlob); e == nil {
					metaBlob = field(pf, 5)
				}
			}
			if len(metaBlob) == 0 {
				continue
			}
			mf, _, e := wireFields(metaBlob)
			if e != nil {
				continue
			}
			if stats, ok := decodeModelUsageStats(field(mf, 9)); ok {
				stepPrompt := stats.input + stats.cacheRead + stats.cacheWrite
				cumInput += stepPrompt
				cumCached += stats.cacheRead
				cumOutput += stats.output
				cumReasoning += stats.thinking
				if stepPrompt > 0 {
					lastStepPrompt = stepPrompt
				}
			}
		}
	}

	if lastInputTokens == 0 && lastStepPrompt > 0 {
		lastInputTokens = lastStepPrompt
	}

	model := responseModel
	if model == "" {
		model = displayModel
	}
	if model == "" {
		model = defaultModel
	}

	if maxWindow == 0 && lastInputTokens > 0 {
		maxWindow = defaultContextWindowForModel(model)
	}

	contextAvailable := lastInputTokens > 0 || maxWindow > 0
	totalTokens := cumInput + cumOutput

	out := map[string]any{
		"context_available": contextAvailable,
		"source":            "native_last_request",
		"provider":          "antigravity",
		"model":             model,
		"default_model":     defaultModel,
		"last_input_tokens": lastInputTokens,
		"context_window":    maxWindow,
		"input_tokens":      cumInput,
		"output_tokens":     cumOutput,
		"cached_tokens":     cumCached,
		"reasoning_tokens":  cumReasoning,
		"total_tokens":      totalTokens,
	}
	return out, nil
}

func (a *Adapter) readSessionUsage(ctx context.Context, sessionID string, readonly bool) (map[string]any, error) {
	if !validID(sessionID) {
		return nil, fmt.Errorf("invalid Antigravity conversation ID")
	}
	if a.remoteHelper != nil {
		return a.remoteHelper(ctx, map[string]any{
			"domain":          "antigravity",
			"action":          "session_usage",
			"conversation_id": sessionID,
			"readonly":        readonly,
		})
	}
	return readNativeSessionUsage(ctx, a.config.Home, sessionID, readonly)
}

func (a *Adapter) resolveDefaultModel(ctx context.Context) string {
	if a.remoteHelper == nil {
		return readDefaultModelForHome(a.config.Home)
	}
	return ""
}
