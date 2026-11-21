package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type convSlotBucket struct {
	Slot15m    int64  `json:"s"`
	Model      string `json:"m"`
	Calls      int64  `json:"c"`
	Input      int64  `json:"in"`
	Output     int64  `json:"out"`
	CacheRead  int64  `json:"cr"`
	CacheWrite int64  `json:"cw"`
	Reasoning  int64  `json:"r"`
}

type cachedConvEntry struct {
	ModUnixNano int64            `json:"mt"`
	Size        int64            `json:"sz"`
	UpdatedUnix int64            `json:"ut"`
	Model       string           `json:"model"`
	Buckets     []convSlotBucket `json:"b"`
}

type diskUsageCache struct {
	Version int                        `json:"version"`
	Entries map[string]cachedConvEntry `json:"entries"`
}

type summaryCatalogMeta struct {
	title     string
	workspace string
}

func loadConversationSummariesMap(ctx context.Context, home string, posixPaths bool) map[string]summaryCatalogMeta {
	out := map[string]summaryCatalogMeta{}
	dbPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "conversation_summaries.db"),
		filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"),
	}
	for _, dbPath := range dbPaths {
		db, err := openNative(dbPath)
		if err != nil {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT conversation_id, title, workspace_uris FROM conversation_summaries WHERE killed=0`)
		if err != nil {
			db.Close()
			continue
		}
		for rows.Next() {
			var id, title, uris string
			if err := rows.Scan(&id, &title, &uris); err != nil || !validID(id) {
				continue
			}
			workspace := ""
			var paths []string
			if json.Unmarshal([]byte(uris), &paths) == nil && len(paths) > 0 {
				if u, e := url.Parse(paths[0]); e == nil && u.Scheme == "file" {
					workspace = u.Path
					if posixPaths {
						if workspace != "" {
							workspace = path.Clean(workspace)
						}
					} else {
						if runtime.GOOS == "windows" {
							workspace = strings.TrimPrefix(workspace, "/")
						}
						workspace = filepath.FromSlash(workspace)
					}
				}
			}
			out[id] = summaryCatalogMeta{
				title:     strings.TrimSpace(title),
				workspace: workspace,
			}
		}
		rows.Close()
		db.Close()
	}
	return out
}

func scanTranscriptStepModels(conversationsDir, id string) map[int]string {
	tPath := filepath.Join(filepath.Dir(conversationsDir), "brain", id, ".system_generated", "logs", "transcript.jsonl")
	st, err := os.Stat(tPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 24*1024*1024 {
		return nil
	}
	f, err := os.Open(tPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	modelsByStep := map[int]string{}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, []byte(`"PLANNER_RESPONSE"`)) && bytes.Contains(line, []byte(`"model"`)) {
			var entry struct {
				StepIndex int    `json:"step_index"`
				Model     string `json:"model"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.StepIndex >= 0 {
				if m := strings.TrimSpace(entry.Model); m != "" {
					modelsByStep[entry.StepIndex] = m
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			break
		}
	}
	if len(modelsByStep) == 0 {
		return nil
	}
	return modelsByStep
}

func scanConversationUsageEntry(ctx context.Context, dbPath, id, defaultModel string, modTime time.Time, fileSize int64) (cachedConvEntry, bool) {
	entry := cachedConvEntry{
		ModUnixNano: modTime.UnixNano(),
		Size:        fileSize,
		UpdatedUnix: modTime.Unix(),
	}
	db, err := openNative(dbPath)
	if err != nil {
		return entry, false
	}
	defer db.Close()

	type idxModel struct {
		idx   int
		model string
	}
	var genModels []idxModel
	var latestGenModel string
	genRows, gErr := db.QueryContext(ctx, `SELECT idx, data FROM gen_metadata ORDER BY idx ASC`)
	if gErr == nil {
		for genRows.Next() {
			var idx int
			var blob []byte
			if genRows.Scan(&idx, &blob) != nil || len(blob) == 0 {
				continue
			}
			fields, _, e := wireFields(blob)
			if e != nil {
				continue
			}
			m := ""
			if cmRaw := field(fields, 1); len(cmRaw) > 0 {
				if cm, _, ce := wireFields(cmRaw); ce == nil {
					m = strings.TrimSpace(string(field(cm, 19)))
					if m == "" {
						m = strings.TrimSpace(string(field(cm, 21)))
					}
				}
			}
			if m == "" {
				if meta3Raw := field(fields, 3); len(meta3Raw) > 0 {
					if m3, _, me := wireFields(meta3Raw); me == nil {
						m = strings.TrimSpace(string(field(m3, 28)))
					}
				}
			}
			if m != "" {
				genModels = append(genModels, idxModel{idx: idx, model: m})
				latestGenModel = m
			}
		}
		genRows.Close()
	}

	transcriptModels := scanTranscriptStepModels(filepath.Dir(dbPath), id)

	type slotKey struct {
		slot15m int64
		model   string
	}
	agg := map[slotKey]*convSlotBucket{}
	var maxStepUnix int64
	var lastModel string

	rows, qErr := db.QueryContext(ctx, `SELECT idx, metadata, CASE WHEN length(metadata) > 0 THEN NULL ELSE step_payload END FROM steps ORDER BY idx ASC`)
	if qErr != nil {
		return entry, false
	}
	defer rows.Close()

	genCursor := 0
	currentGenModel := ""
	for rows.Next() {
		var idx int
		var metaBlob, payloadBlob []byte
		if rows.Scan(&idx, &metaBlob, &payloadBlob) != nil {
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
		stats, ok := decodeModelUsageStats(field(mf, 9))
		if !ok {
			continue
		}

		stepUnix := modTime.Unix()
		if rawTS := field(mf, 1); len(rawTS) > 0 {
			if _, tsNums, te := wireFields(rawTS); te == nil && int64(tsNums[1]) > 0 {
				stepUnix = int64(tsNums[1])
			}
		}
		if stepUnix > maxStepUnix {
			maxStepUnix = stepUnix
		}

		for genCursor < len(genModels) && genModels[genCursor].idx <= idx {
			currentGenModel = genModels[genCursor].model
			genCursor++
		}

		stepModel := ""
		if transcriptModels != nil {
			stepModel = transcriptModels[idx]
		}
		if stepModel == "" {
			stepModel = currentGenModel
		}
		if stepModel == "" {
			stepModel = latestGenModel
		}
		if stepModel == "" {
			stepModel = defaultModel
		}
		if stepModel == "" {
			stepModel = "antigravity-default"
		}
		lastModel = stepModel

		slot := stepUnix / 900
		sk := slotKey{slot15m: slot, model: stepModel}
		b := agg[sk]
		if b == nil {
			b = &convSlotBucket{Slot15m: slot, Model: stepModel}
			agg[sk] = b
		}
		b.Calls++
		b.Input += stats.input
		b.Output += stats.output
		b.CacheRead += stats.cacheRead
		b.CacheWrite += stats.cacheWrite
		b.Reasoning += stats.thinking
	}

	if maxStepUnix > entry.UpdatedUnix {
		entry.UpdatedUnix = maxStepUnix
	}
	if lastModel != "" {
		entry.Model = lastModel
	} else if latestGenModel != "" {
		entry.Model = latestGenModel
	} else {
		entry.Model = defaultModel
	}

	if len(agg) > 0 {
		buckets := make([]convSlotBucket, 0, len(agg))
		for _, b := range agg {
			buckets = append(buckets, *b)
		}
		sort.Slice(buckets, func(i, j int) bool {
			if buckets[i].Slot15m != buckets[j].Slot15m {
				return buckets[i].Slot15m < buckets[j].Slot15m
			}
			return buckets[i].Model < buckets[j].Model
		})
		entry.Buckets = buckets
	}
	return entry, true
}

func collectConversationFiles(home string) map[string]struct {
	path string
	info os.FileInfo
} {
	found := map[string]struct {
		path string
		info os.FileInfo
	}{}
	dirs := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "conversations"),
		filepath.Join(home, ".gemini", "antigravity", "conversations"),
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".db")
			if !validID(id) {
				continue
			}
			info, sErr := e.Info()
			if sErr != nil || !info.Mode().IsRegular() {
				continue
			}
			full := filepath.Join(d, e.Name())
			if prev, exists := found[id]; exists && !info.ModTime().After(prev.info.ModTime()) {
				continue
			}
			found[id] = struct {
				path string
				info os.FileInfo
			}{path: full, info: info}
		}
	}
	return found
}

func readUsageSummaryWithCache(ctx context.Context, home string, sinceMs, untilMs int64, posixPaths bool, cache map[string]cachedConvEntry) (map[string]any, bool, error) {
	defaultModel := readDefaultModelForHome(home)
	catalogMeta := loadConversationSummariesMap(ctx, home, posixPaths)
	convFiles := collectConversationFiles(home)

	dirty := false
	present := make(map[string]bool, len(convFiles))

	type slotAggKey struct {
		slot15m int64
		model   string
	}
	globalSlots := map[slotAggKey]*convSlotBucket{}

	type sessionSummary struct {
		id         string
		title      string
		workspace  string
		model      string
		updatedAt  int64
		calls      int64
		input      int64
		output     int64
		cacheRead  int64
		cacheWrite int64
		reasoning  int64
		total      int64
	}
	var sessions []sessionSummary

	for id, cf := range convFiles {
		if ctx.Err() != nil {
			return nil, dirty, ctx.Err()
		}
		present[id] = true
		entry, hit := cache[id]
		if !hit || entry.ModUnixNano != cf.info.ModTime().UnixNano() || entry.Size != cf.info.Size() {
			scanned, ok := scanConversationUsageEntry(ctx, cf.path, id, defaultModel, cf.info.ModTime().UTC(), cf.info.Size())
			if !ok {
				continue
			}
			entry = scanned
			cache[id] = entry
			dirty = true
		}
		if len(entry.Buckets) == 0 {
			continue
		}

		var sCalls, sIn, sOut, sCR, sCW, sReason int64
		var sModel string
		var sMaxSlot int64
		for _, b := range entry.Buckets {
			slotStartMs := b.Slot15m * 900 * 1000
			slotEndMs := (b.Slot15m + 1) * 900 * 1000
			if sinceMs > 0 && slotEndMs <= sinceMs {
				continue
			}
			if untilMs > 0 && slotStartMs >= untilMs {
				continue
			}
			tot := b.Input + b.Output + b.CacheRead + b.CacheWrite
			if tot <= 0 && b.Calls <= 0 {
				continue
			}

			sk := slotAggKey{slot15m: b.Slot15m, model: b.Model}
			gb := globalSlots[sk]
			if gb == nil {
				gb = &convSlotBucket{Slot15m: b.Slot15m, Model: b.Model}
				globalSlots[sk] = gb
			}
			gb.Calls += b.Calls
			gb.Input += b.Input
			gb.Output += b.Output
			gb.CacheRead += b.CacheRead
			gb.CacheWrite += b.CacheWrite
			gb.Reasoning += b.Reasoning

			sCalls += b.Calls
			sIn += b.Input
			sOut += b.Output
			sCR += b.CacheRead
			sCW += b.CacheWrite
			sReason += b.Reasoning
			if b.Slot15m >= sMaxSlot {
				sMaxSlot = b.Slot15m
				sModel = b.Model
			}
		}

		sTot := sIn + sOut + sCR + sCW
		if sTot <= 0 && sCalls <= 0 {
			continue
		}
		if sModel == "" {
			sModel = entry.Model
		}
		meta := catalogMeta[id]
		updated := entry.UpdatedUnix
		if sMaxSlot*900 > updated {
			updated = sMaxSlot * 900
		}
		sessions = append(sessions, sessionSummary{
			id:         id,
			title:      meta.title,
			workspace:  meta.workspace,
			model:      sModel,
			updatedAt:  updated,
			calls:      sCalls,
			input:      sIn,
			output:     sOut,
			cacheRead:  sCR,
			cacheWrite: sCW,
			reasoning:  sReason,
			total:      sTot,
		})
	}

	for cachedID := range cache {
		if !present[cachedID] {
			delete(cache, cachedID)
			dirty = true
		}
	}

	slotList := make([]map[string]any, 0, len(globalSlots))
	keys := make([]slotAggKey, 0, len(globalSlots))
	for k := range globalSlots {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].slot15m != keys[j].slot15m {
			return keys[i].slot15m < keys[j].slot15m
		}
		return keys[i].model < keys[j].model
	})
	for _, k := range keys {
		b := globalSlots[k]
		slotList = append(slotList, map[string]any{
			"slot_15m":    b.Slot15m,
			"model":       b.Model,
			"calls":       b.Calls,
			"input":       b.Input,
			"output":      b.Output,
			"cache_read":  b.CacheRead,
			"cache_write": b.CacheWrite,
			"reasoning":   b.Reasoning,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].total != sessions[j].total {
			return sessions[i].total > sessions[j].total
		}
		return sessions[i].updatedAt > sessions[j].updatedAt
	})
	activeSessions := len(sessions)
	if len(sessions) > 50 {
		sessions = sessions[:50]
	}
	sessionRows := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		sessionRows = append(sessionRows, map[string]any{
			"session_id":  s.id,
			"title":       s.title,
			"workspace":   s.workspace,
			"model":       s.model,
			"updated_at":  time.Unix(s.updatedAt, 0).UTC().Format(time.RFC3339),
			"calls":       s.calls,
			"input":       s.input,
			"output":      s.output,
			"cache_read":  s.cacheRead,
			"cache_write": s.cacheWrite,
			"reasoning":   s.reasoning,
			"total":       s.total,
		})
	}

	return map[string]any{
		"provider":        "antigravity",
		"slots":           slotList,
		"sessions":        sessionRows,
		"active_sessions": activeSessions,
	}, dirty, nil
}

func readHelperUsageSummary(ctx context.Context, home string, sinceMs, untilMs int64) (map[string]any, error) {
	cacheDir := filepath.Join(home, ".cache", "astrorder")
	cachePath := filepath.Join(cacheDir, "agy-usage-cache-v1.json")
	dc := diskUsageCache{Version: 1, Entries: map[string]cachedConvEntry{}}
	if raw, err := os.ReadFile(cachePath); err == nil {
		var loaded diskUsageCache
		if json.Unmarshal(raw, &loaded) == nil && loaded.Version == 1 && loaded.Entries != nil {
			dc.Entries = loaded.Entries
		}
	}

	res, dirty, err := readUsageSummaryWithCache(ctx, home, sinceMs, untilMs, true, dc.Entries)
	if err != nil {
		return nil, err
	}
	if dirty {
		if mkErr := os.MkdirAll(cacheDir, 0o700); mkErr == nil {
			if encoded, mErr := json.Marshal(dc); mErr == nil {
				tmpFile, tErr := os.CreateTemp(cacheDir, ".agy-usage-*.tmp")
				if tErr == nil {
					tmpName := tmpFile.Name()
					_, wErr := tmpFile.Write(encoded)
					cErr := tmpFile.Close()
					if wErr == nil && cErr == nil {
						_ = os.Chmod(tmpName, 0o600)
						_ = os.Rename(tmpName, cachePath)
					} else {
						_ = os.Remove(tmpName)
					}
				}
			}
		}
	}
	return res, nil
}

func (a *Adapter) readUsageSummary(ctx context.Context, sinceMs, untilMs int64) (map[string]any, error) {
	if a.remoteHelper != nil {
		return a.remoteHelper(ctx, map[string]any{
			"domain":   "antigravity",
			"action":   "usage_summary",
			"since_ms": sinceMs,
			"until_ms": untilMs,
		})
	}
	a.usageCacheMu.Lock()
	if a.usageCache == nil {
		a.usageCache = map[string]cachedConvEntry{}
	}
	localCopy := make(map[string]cachedConvEntry, len(a.usageCache))
	for k, v := range a.usageCache {
		localCopy[k] = v
	}
	a.usageCacheMu.Unlock()

	res, dirty, err := readUsageSummaryWithCache(ctx, a.config.Home, sinceMs, untilMs, false, localCopy)
	if err != nil {
		return nil, err
	}
	if dirty {
		a.usageCacheMu.Lock()
		a.usageCache = localCopy
		a.usageCacheMu.Unlock()
	}
	return res, nil
}
