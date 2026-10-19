package antigravity

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	db, err := openNative(filepath.Join(a.config.Home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
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
		t := map[string]any{"id": item["id"], "name": name, "status": status, "input": input}
		if raw := field(fields, 31); len(raw) > 0 {
			details, _, e := wireFields(raw)
			if e != nil {
				return nil, e
			}
			for _, n := range []protowire.Number{1, 9, 2, 3} {
				if message := string(field(details, n)); message != "" {
					t["error"] = message
					t["status"] = "error"
					break
				}
			}
		}
		if kind == 21 {
			d, _, e := wireFields(field(fields, 28))
			if e != nil {
				return nil, e
			}
			output, _, e := wireFields(field(d, 21))
			if e != nil {
				return nil, e
			}
			t["output"] = string(field(output, 1))
			if t["output"] == "" {
				t["output"] = string(field(d, 4)) + string(field(d, 5))
			}
		}
		item["role"] = "tool"
		item["kind"] = "tool"
		item["tool"] = t
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
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, next, nil
}
