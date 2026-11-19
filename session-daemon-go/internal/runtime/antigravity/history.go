package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

var conversationID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validID(s string) bool { return conversationID.MatchString(s) }
func itemID(s string, idx int, suffix string) string {
	return fmt.Sprintf("antigravity:%s:%d%s", s, idx, suffix)
}
func (a *Adapter) historyDir() string {
	return filepath.Join(a.config.Home, ".gemini", "antigravity-cli", "conversations")
}
func openNative(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(p)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func (a *Adapter) validateResume(ctx context.Context, id string) error {
	return a.validateResumeState(ctx, id, false)
}

func (a *Adapter) validateResumeState(ctx context.Context, id string, exitedOwner bool) error {
	if a.remoteHelper != nil {
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":          "antigravity",
			"action":          "inspect_conversation",
			"conversation_id": id,
			"exited_owner":    exitedOwner,
		})
		if err != nil {
			return err
		}
		if res["desktop_readonly"] == true {
			return fmt.Errorf("Antigravity desktop conversation is history-only")
		}
		return nil
	}
	return validateResumeStateForHome(ctx, a.config.Home, id, exitedOwner)
}

func (a *Adapter) inspectResumeTarget(ctx context.Context, id string, exitedOwner bool) (bool, error) {
	if a.remoteHelper != nil {
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":          "antigravity",
			"action":          "inspect_conversation",
			"conversation_id": id,
			"exited_owner":    exitedOwner,
		})
		if err != nil {
			return false, err
		}
		return res["desktop_readonly"] == true, nil
	}
	return inspectResumeForHome(ctx, a.config.Home, id, exitedOwner)
}

func inspectResumeForHome(ctx context.Context, home, id string, exitedOwner bool) (bool, error) {
	cliPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", id+".db")
	if _, err := os.Stat(cliPath); os.IsNotExist(err) {
		desktopPath := filepath.Join(home, ".gemini", "antigravity", "conversations", id+".db")
		if _, err := os.Stat(desktopPath); err == nil {
			return true, nil
		}
	}
	return false, validateResumeStateForHome(ctx, home, id, exitedOwner)
}

func validateResumeStateForHome(ctx context.Context, home, id string, exitedOwner bool) error {
	db, err := openNative(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	var active, killed bool
	if err = db.QueryRowContext(ctx, `SELECT not_fully_idle,killed FROM conversation_summaries WHERE conversation_id=?`, id).Scan(&active, &killed); err != nil {
		return err
	}
	// agy leaves not_fully_idle set after an abrupt transport exit. Ignore it
	// only for our own reaped owner, never for an externally active session.
	if active && !exitedOwner || killed {
		return fmt.Errorf("Antigravity native conversation is active or unavailable")
	}
	return nil
}

func isConversationNotFullyIdleForHome(ctx context.Context, home, id string) (bool, bool) {
	if !validID(id) || strings.TrimSpace(home) == "" {
		return false, false
	}
	db, err := openNative(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if err != nil {
		return false, false
	}
	defer db.Close()
	var active, killed bool
	if err = db.QueryRowContext(ctx, `SELECT not_fully_idle,killed FROM conversation_summaries WHERE conversation_id=?`, id).Scan(&active, &killed); err != nil {
		return false, false
	}
	return active && !killed, true
}

func (a *Adapter) isConversationStillActive(ctx context.Context, id string) bool {
	if !validID(id) {
		return false
	}
	if a.remoteHelper != nil {
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":          "antigravity",
			"action":          "conversation_active",
			"conversation_id": id,
		})
		if err != nil {
			return false
		}
		return res["active"] == true
	}
	active, _ := isConversationNotFullyIdleForHome(ctx, a.config.Home, id)
	return active
}

func (a *Adapter) readSessionHistory(ctx context.Context, id, before string, limit int, readonly bool) ([]map[string]any, any, error) {
	if a.remoteHelper != nil {
		if !validID(id) {
			return nil, nil, fmt.Errorf("invalid Antigravity conversation ID")
		}
		if limit < 1 || limit > 200 {
			return nil, nil, fmt.Errorf("invalid Antigravity history limit")
		}
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":          "antigravity",
			"action":          "history_page",
			"conversation_id": id,
			"before":          before,
			"limit":           limit,
			"readonly":        readonly,
		})
		if err != nil {
			return nil, nil, err
		}
		items := normalizeMapSlice(res["items"])
		var next any
		if rawNext, exists := res["next_cursor"]; exists && rawNext != nil {
			next = fmt.Sprint(rawNext)
		}
		return items, next, nil
	}
	dir := a.historyDir()
	if readonly {
		dir = filepath.Join(a.config.Home, ".gemini", "antigravity", "conversations")
	}
	return readHistory(ctx, dir, id, before, limit)
}

// wireFields validates the complete protobuf wire message. Field numbers come
// from agy 1.2.4's embedded gemini_coder.Step / exa.cortex_pb descriptors.
func wireFields(b []byte) (map[protowire.Number][][]byte, map[protowire.Number]uint64, error) {
	data := map[protowire.Number][][]byte{}
	nums := map[protowire.Number]uint64{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, nil, fmt.Errorf("invalid Antigravity protobuf tag")
		}
		b = b[n:]
		switch typ {
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil, nil, fmt.Errorf("invalid Antigravity protobuf bytes")
			}
			data[num] = append(data[num], v)
			b = b[n:]
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, nil, fmt.Errorf("invalid Antigravity protobuf integer")
			}
			nums[num] = v
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, nil, fmt.Errorf("invalid Antigravity protobuf field")
			}
			b = b[n:]
		}
	}
	return data, nums, nil
}
func field(d map[protowire.Number][][]byte, n protowire.Number) []byte {
	if len(d[n]) > 0 {
		return d[n][0]
	}
	return nil
}
func decodeStep(id string, idx, kind, state int, b, metadata []byte) (map[string]any, error) {
	fields, nums, err := wireFields(b)
	if err != nil {
		return nil, err
	}
	if int(nums[1]) != kind {
		return nil, fmt.Errorf("Antigravity native step type mismatch")
	}
	if len(metadata) == 0 {
		metadata = field(fields, 5)
	}
	meta, _, err := wireFields(metadata)
	if err != nil {
		return nil, err
	}
	stamp := ""
	if raw := field(meta, 1); len(raw) > 0 {
		_, ts, e := wireFields(raw)
		if e != nil {
			return nil, e
		}
		stamp = time.Unix(int64(ts[1]), int64(ts[2])).UTC().Format(time.RFC3339Nano)
	}
	item := map[string]any{"id": itemID(id, idx, ""), "role": "assistant", "kind": "message", "text": "", "attachments": []any{}, "created_at": stamp}
	switch kind {
	case 14:
		d, _, e := wireFields(field(fields, 19))
		if e != nil {
			return nil, e
		}
		text := string(field(d, 1))
		if text == "" {
			for _, part := range d[3] {
				v, _, e := wireFields(part)
				if e != nil {
					return nil, e
				}
				text += string(field(v, 1))
			}
		}
		if text == "" {
			text = string(field(d, 2))
		}
		item["role"] = "user"
		item["text"] = text
	case 15:
		d, _, e := wireFields(field(fields, 20))
		if e != nil {
			return nil, e
		}
		text := field(d, 8)
		if len(text) == 0 {
			text = field(d, 1)
		}
		item["text"] = string(text)
		item["thinking"] = string(field(d, 3))
	default:
		tool, _, e := wireFields(field(meta, 4))
		if e != nil {
			return nil, e
		}
		name := string(field(tool, 2))
		if name == "" {
			return nil, nil
		}
		input := string(field(tool, 3))
		status := "running"
		switch state {
		case 3:
			status = "completed"
		case 6, 7, 12:
			status = "error"
		}
		rawError := ""
		if raw := field(fields, 31); len(raw) > 0 {
			details, _, e := wireFields(raw)
			if e != nil {
				return nil, e
			}
			for _, n := range []protowire.Number{1, 9, 2, 3} {
				if message := string(field(details, n)); message != "" {
					rawError = message
					status = "error"
					break
				}
			}
		}
		rawOutput := ""
		if kind == 21 {
			d, _, e := wireFields(field(fields, 28))
			if e != nil {
				return nil, e
			}
			output, _, e := wireFields(field(d, 21))
			if e != nil {
				return nil, e
			}
			rawOutput = string(field(output, 1))
			if rawOutput == "" {
				rawOutput = string(field(d, 4)) + string(field(d, 5))
			}
		}

		extraArgs := map[string]any{}
		if raw140 := field(fields, 140); len(raw140) > 0 {
			if d140, _, e := wireFields(raw140); e == nil {
				for _, kvRaw := range d140[1] {
					if kv, _, ke := wireFields(kvRaw); ke == nil {
						k := strings.TrimSpace(string(field(kv, 1)))
						v := string(field(kv, 2))
						if k == "" {
							continue
						}
						if k == "ReplacementChunks" && strings.HasPrefix(strings.TrimSpace(v), "[") {
							var parsed []any
							if json.Unmarshal([]byte(v), &parsed) == nil {
								extraArgs[k] = parsed
								continue
							}
						}
						extraArgs[k] = v
					}
				}
				if rawOut := field(d140, 2); len(rawOut) > 0 {
					if res140, _, re := wireFields(rawOut); re == nil && (len(field(res140, 1)) > 0 || len(field(res140, 2)) > 0 || len(field(res140, 3)) > 0 || len(field(res140, 6)) > 0) {
						if outText := string(field(res140, 1)); outText != "" && rawOutput == "" {
							rawOutput = outText
						}
						if errText := string(field(res140, 2)); errText != "" && rawError == "" && status == "error" {
							rawError = errText
						}
					} else if utf8.Valid(rawOut) && rawOutput == "" {
						rawOutput = string(rawOut)
					}
				}
			}
		}

		var bgTask map[string]any
		if raw148 := field(fields, 148); len(raw148) > 0 {
			if d148, _, e := wireFields(raw148); e == nil {
				bgTask = map[string]any{
					"task_id": string(field(d148, 1)),
					"output":  string(field(d148, 3)),
					"command": string(field(d148, 4)),
					"summary": string(field(d148, 7)),
				}
			}
		}

		t, text := normalizeAntigravityTool(fmt.Sprint(item["id"]), name, status, input, rawOutput, rawError, extraArgs, bgTask)
		if name == "ask_question" && status != "error" {
			tArgs, _ := t["arguments"].(map[string]any)
			if questions, summaryText := extractAntigravityQuestions(tArgs); len(questions) > 0 {
				item["role"] = "assistant"
				item["kind"] = "message"
				item["delivery"] = "async"
				item["questions"] = questions
				item["answered"] = status == "completed"
				item["text"] = summaryText
				return item, nil
			}
		}
		item["role"] = "tool"
		item["kind"] = "tool"
		item["tool"] = t
		item["text"] = text
	}
	return item, nil
}
func readHistory(ctx context.Context, dir, id, before string, limit int) ([]map[string]any, any, error) {
	if !validID(id) {
		return nil, nil, fmt.Errorf("invalid Antigravity conversation ID")
	}
	if limit < 1 || limit > 200 {
		return nil, nil, fmt.Errorf("invalid Antigravity history limit")
	}
	boundary := int64(1 << 62)
	if before != "" {
		var err error
		boundary, err = strconv.ParseInt(before, 10, 64)
		if err != nil || boundary < 0 {
			return nil, nil, fmt.Errorf("invalid Antigravity history cursor")
		}
	}
	db, err := openNative(filepath.Join(dir, id+".db"))
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT idx,step_type,status,step_format,step_payload,metadata FROM steps WHERE idx<? ORDER BY idx DESC`, boundary)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	userByIdx := map[int]map[string]any{}
	var next any
	for rows.Next() {
		var idx, kind, state, format int
		var payload, meta []byte
		if err = rows.Scan(&idx, &kind, &state, &format, &payload, &meta); err != nil {
			return nil, nil, err
		}
		if format != 0 {
			return nil, nil, fmt.Errorf("unsupported Antigravity native step format %d", format)
		}
		item, e := decodeStep(id, idx, kind, state, payload, meta)
		if e != nil {
			return nil, nil, e
		}
		if item == nil {
			continue
		}
		if len(items) == limit {
			next = strings.TrimPrefix(fmt.Sprint(items[len(items)-1]["id"]), "antigravity:"+id+":")
			break
		}
		if kind == 14 {
			userByIdx[idx] = item
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(userByIdx) > 0 {
		enrichTranscriptUserMedia(dir, id, userByIdx)
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, next, nil
}

func resolveMediaFilePath(rawURI string) string {
	trimmed := strings.TrimSpace(rawURI)
	if trimmed == "" || strings.ContainsAny(trimmed, "\x00\r\n") {
		return ""
	}
	if strings.HasPrefix(trimmed, "file://") {
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme != "file" {
			return ""
		}
		p := u.Path
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && ((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) && p[2] == ':' {
			p = p[1:]
		}
		return filepath.FromSlash(p)
	}
	return filepath.FromSlash(trimmed)
}

func enrichTranscriptUserMedia(conversationsDir, id string, userByIdx map[int]map[string]any) {
	brainDir := filepath.Join(filepath.Dir(conversationsDir), "brain", id)
	realBrainDir, err := filepath.EvalSymlinks(brainDir)
	if err != nil {
		return
	}
	transcriptPath := filepath.Join(realBrainDir, ".system_generated", "logs", "transcript.jsonl")
	f, err := os.Open(transcriptPath)
	if err != nil {
		return
	}
	defer f.Close()

	const (
		maxSingleImageBytes = 3 * 1024 * 1024
		maxPageInlineBytes  = 6 * 1024 * 1024
	)
	var totalInlineBytes int64
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, []byte(`"USER_INPUT"`)) && bytes.Contains(line, []byte(`"media"`)) {
			var entry struct {
				StepIndex int    `json:"step_index"`
				Type      string `json:"type"`
				Media     []struct {
					MimeType string `json:"mime_type"`
					URI      string `json:"uri"`
				} `json:"media"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Type == "USER_INPUT" && len(entry.Media) > 0 {
				if item := userByIdx[entry.StepIndex]; item != nil {
					atts := []any{}
					for mIdx, m := range entry.Media {
						mime := strings.ToLower(strings.TrimSpace(m.MimeType))
						switch mime {
						case "image/png", "image/jpeg", "image/gif", "image/webp":
						default:
							continue
						}
						candidatePath := resolveMediaFilePath(m.URI)
						if candidatePath == "" {
							continue
						}
						realMediaPath, symErr := filepath.EvalSymlinks(candidatePath)
						if symErr != nil {
							continue
						}
						rel, relErr := filepath.Rel(realBrainDir, realMediaPath)
						if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
							continue
						}
						st, statErr := os.Stat(realMediaPath)
						if statErr != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxSingleImageBytes {
							continue
						}
						if totalInlineBytes+st.Size() > maxPageInlineBytes {
							break
						}
						rawBytes, fileErr := os.ReadFile(realMediaPath)
						if fileErr != nil || int64(len(rawBytes)) > maxSingleImageBytes {
							continue
						}
						totalInlineBytes += int64(len(rawBytes))
						atts = append(atts, map[string]any{
							"id":         fmt.Sprintf("antigravity-media-%s-%d-%d", id, entry.StepIndex, mIdx),
							"name":       filepath.Base(realMediaPath),
							"media_type": mime,
							"url":        "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(rawBytes),
						})
					}
					if len(atts) > 0 {
						item["attachments"] = atts
					}
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
}
