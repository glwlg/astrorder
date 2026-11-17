// Package artifacts maintains the platform-owned group artifact directory.
// Paths keep their first verified source. Callers verify files and native
// changes before publishing; parsing a path alone never publishes an artifact.
package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const Key = "artifacts"
const ContentPrefix = "artifacts."

type File struct {
	ID           string   `json:"id"`
	GroupID      string   `json:"group_id"`
	MemberID     string   `json:"member_id"`
	MemberName   string   `json:"member_name"`
	AgentID      string   `json:"agent_id"`
	SessionID    string   `json:"session_id"`
	ConnectionID string   `json:"connection_id"`
	Workspace    string   `json:"workspace"`
	Path         string   `json:"path"`
	Aliases      []string `json:"aliases"`
	Name         string   `json:"name"`
	ContentKey   string   `json:"content_key"`
	CreatedAt    string   `json:"created_at"`
	EvidenceID   string   `json:"evidence_id,omitempty"`
}

type Directory struct {
	Items []File `json:"items"`
}

func DatabaseKey(groupID string) string { return "blackboard:group:" + groupID + ":" + Key }
func Reserved(namespace, key string) bool {
	return strings.HasPrefix(namespace, "group:") && (key == Key || strings.HasPrefix(key, ContentPrefix))
}

// Normalize strips presentation-only line/fragment suffixes. It does not infer a host.
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "file://") {
		if fileURL, err := url.Parse(raw); err == nil {
			raw = fileURL.EscapedPath()
			if fileURL.Host != "" && fileURL.Host != "localhost" {
				raw = "//" + fileURL.Host + raw
			}
			if len(raw) > 3 && raw[0] == '/' && raw[2] == ':' {
				raw = raw[1:]
			}
		}
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = lineSuffix.ReplaceAllString(raw, "")
	if at := strings.IndexAny(raw, "#?"); at >= 0 {
		raw = raw[:at]
	}
	if raw == "" {
		return ""
	}
	unc := strings.HasPrefix(raw, "//")
	raw = path.Clean(raw)
	if unc {
		raw = "/" + raw
	}
	if len(raw) > 2 && raw[1] == ':' {
		raw = strings.ToLower(raw[:1]) + raw[1:]
	}
	return raw
}

var lineSuffix = regexp.MustCompile(`:\d+(?::\d+)?$`)
var scheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
var markdown = regexp.MustCompile(`!?\[[^\]\n]*\]\(\s*(<[^>\n]+>|[^\s\n]+?)(?:\s+"[^"\n]*")?\s*\)`)
var inline = regexp.MustCompile("`([^`\\n]+)`")
var bare = regexp.MustCompile(`(?:file:///|[A-Za-z]:[\\/]|\\\\|/|\./|\.\./)[^\s<>\x60"'|，。；！？（）【】]+`)
var relative = regexp.MustCompile(`(?:[\p{L}\p{N}_.-]+/)+[^\s<>\x60"'|，。；！？（）【】]+|[\p{L}\p{N}_-]+\.[A-Za-z][A-Za-z0-9]{0,9}`)

func plausible(raw string) bool {
	windowsPath := len(raw) > 2 && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\')
	if !windowsPath && scheme.MatchString(lineSuffix.ReplaceAllString(raw, "")) && !strings.HasPrefix(strings.ToLower(raw), "file://") {
		return false
	}
	if strings.ContainsAny(raw, "\x00\r\n$*{}[]|") || strings.HasPrefix(raw, "-") || len(raw) > 8192 || strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, "\\") {
		return false
	}
	p := Normalize(raw)
	base := path.Base(p)
	return p != "" && strings.Trim(base, ".") != "" && (strings.Contains(base, ".") || strings.ToUpper(base) == base && len(base) > 1 && strings.Contains(p, "/"))
}

// Candidates preserves source order across links, inline paths and plain paths.
// Fenced code bodies are not prose references to output files.
func Candidates(text string) []string {
	var out []string
	seen := map[string]bool{}
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			token := trimmed[:3]
			if fence == "" {
				fence = token
			} else if fence == token {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		// Process structured references before bare tokens to preserve filenames with spaces.
		for len(line) > 0 {
			link := markdown.FindStringSubmatchIndex(line)
			code := inline.FindStringSubmatchIndex(line)
			start, end, from, to := len(line), len(line), -1, -1
			if link != nil {
				start, end, from, to = link[0], link[1], link[2], link[3]
			}
			if code != nil && code[0] < start {
				start, end, from, to = code[0], code[1], code[2], code[3]
			}
			add := func(raw string) {
				raw = strings.Trim(raw, "<>")
				raw = strings.TrimRight(raw, ").,;!]")
				key := Normalize(raw)
				if plausible(raw) && !seen[key] {
					seen[key] = true
					out = append(out, raw)
				}
			}
			// Mask web URLs so / segments and host names cannot become local artifacts.
			prefix := jsonExpression.ReplaceAllString(webURL.ReplaceAllString(line[:start], " "), " ")
			spans := bare.FindAllStringIndex(prefix, -1)
			for _, span := range spans {
				add(prefix[span[0]:span[1]])
			}
			prefix = bare.ReplaceAllString(prefix, " ")
			for _, match := range relative.FindAllString(prefix, -1) {
				add(match)
			}
			if from < 0 {
				break
			}
			add(line[from:to])
			line = line[end:]
		}
	}
	return out
}

var webURL = regexp.MustCompile(`(?i)(?:https?|ssh|git|data|mailto):[^\s<>]+`)
var jsonExpression = regexp.MustCompile(`\$(?:\.[\p{L}\p{N}_-]+|\[[^\]\n]*\])+`)

func ReadTx(ctx context.Context, tx *sql.Tx, groupID string) (Directory, error) {
	d := Directory{Items: []File{}}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE key=?`, DatabaseKey(groupID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(raw), &d)
	return d, err
}

func Absolute(p string) bool {
	return strings.HasPrefix(p, "/") || len(p) > 2 && p[1] == ':' && p[2] == '/'
}
func Equal(a, b string) bool {
	if len(a) > 2 && a[1] == ':' || strings.HasPrefix(a, "//") {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func Find(d Directory, raw string) *File {
	candidate := Normalize(raw)
	for _, item := range d.Items {
		if Equal(candidate, item.Path) {
			copy := item
			return &copy
		}
		for _, alias := range item.Aliases {
			if Equal(candidate, alias) {
				copy := item
				return &copy
			}
		}
	}
	return nil
}

// Reference constructs metadata only. The source must come from the exact
// Agent/session record, and the caller must stat Path on that connection.
func Reference(source File, raw string) File {
	p := Normalize(raw)
	if !Absolute(p) {
		p = Normalize(source.Workspace + "/" + p)
	}
	hash := sha256.Sum256([]byte(source.GroupID + "\x00" + p))
	source.ID = "file-" + hex.EncodeToString(hash[:12])
	source.Path, source.Name, source.ContentKey = p, path.Base(p), ""
	source.Aliases = []string{Normalize(raw)}
	root := strings.TrimRight(Normalize(source.Workspace), "/") + "/"
	if relative := strings.TrimPrefix(p, root); strings.HasPrefix(p, root) && !Equal(relative, Normalize(raw)) {
		source.Aliases = append(source.Aliases, relative)
	}
	return source
}

func WriteTx(ctx context.Context, tx *sql.Tx, groupID string, d Directory) error {
	if d.Items == nil {
		d.Items = []File{}
	}
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, DatabaseKey(groupID), string(body))
	return err
}
