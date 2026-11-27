// Package preferences implements the P02 workspace preference contract.
package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/store"
)

// Patch preserves list field presence with pointers and accepts JSON-shaped maps.
type Patch struct {
	Appearance     map[string]any
	SessionPins    map[string]any
	PinnedProjects *[]string
	ProjectOrder   *[]string
}

// State preserves existing decoded JSON values; Patch validates new writes.
type State struct {
	Appearance     map[string]any `json:"appearance"`
	SessionPins    map[string]any `json:"session_pins"`
	PinnedProjects any            `json:"pinned_projects"`
	ProjectOrder   any            `json:"project_order"`
}

var colors = set(
	"", "red", "orange", "amber", "yellow", "lime", "green", "emerald", "teal", "cyan",
	"sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink", "rose", "slate",
	"coral", "salmon", "terracotta", "mint", "turquoise", "ocean", "periwinkle",
	"lavender", "grape", "berry", "mulberry", "graphite",
)
var icons = set(
	"folder", "rocket", "tools", "database", "cloud", "code", "package", "book", "bug",
	"flame", "zap", "star", "heart", "globe", "server", "terminal", "lightbulb", "shield",
	"target", "gear", "coffee", "cube", "diamond", "planet", "sparkles", "atom", "brain",
	"cpu", "network", "git-branch", "git-pull-request", "braces", "chart", "activity",
	"dashboard", "flask", "leaf", "flower", "palette", "music", "camera", "puzzle",
	"glass-orbit", "glass-prism", "glass-rocket", "glass-layers", "glass-cube", "glass-flower",
)

// Illustrated assets carry their own palette; their accent also colors running sessions.
var artIconColors = map[string]string{
	"glass-orbit": "blue", "glass-prism": "violet", "glass-rocket": "red",
	"glass-layers": "emerald", "glass-cube": "cyan", "glass-flower": "fuchsia",
}

const (
	// DefaultMaxValueBytes caps single-row value size returned by unfiltered Query calls
	// when the caller did not explicitly request that key or prefix.
	DefaultMaxValueBytes = 256 * 1024
)

var (
	p02ExactKeys = []string{"appearance", "session_pins", "pinned_projects", "project_order"}
	p02Prefixes  = []string{"appearance:", "session_pins:", "pinned_projects:", "project_order:"}

	// DefaultExcludedKeys are giant non-core preference keys skipped by default unless
	// explicitly requested by key or matching prefix.
	DefaultExcludedKeys = []string{"meetings:records:v1", "meetings:records"}

	// DefaultExcludedPrefixes are heavy key prefixes skipped by default unless
	// explicitly requested by key or matching prefix.
	DefaultExcludedPrefixes = []string{"meetings:records:"}
)

// QueryOptions filters workspace_preferences rows using indexed SQL WHERE predicates.
// When Keys and Prefixes are both empty, DefaultExcludedKeys, DefaultExcludedPrefixes,
// and oversized rows (larger than MaxValueBytes / DefaultMaxValueBytes) are excluded
// unless IncludeHeavyKeys is true. When a caller explicitly names a key in Keys or a
// matching prefix in Prefixes, matching rows are returned in full regardless of size.
type QueryOptions struct {
	Keys             []string
	Prefixes         []string
	IncludeHeavyKeys bool
	MaxValueBytes    int
}

// Read returns only the four public P02 namespaces; tombstone appearance values are omitted.
func Read(ctx context.Context, st *store.Store) (State, error) {
	return readPreferences(ctx, st.DB())
}

// ReadKey returns one preference by exact primary-key lookup (including heavy blobs such
// as meetings:records:v1 when explicitly requested).
func ReadKey(ctx context.Context, st *store.Store, key string) (any, bool, error) {
	if st == nil || key == "" {
		return nil, false, nil
	}
	return readKey(ctx, st.DB(), key)
}

// Query returns workspace_preferences rows matching opts using indexed SQL filters.
func Query(ctx context.Context, st *store.Store, opts QueryOptions) (map[string]any, error) {
	if st == nil {
		return map[string]any{}, nil
	}
	return queryPreferences(ctx, st.DB(), opts)
}

type preferenceQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func prefixUpperBound(prefix string) (string, bool) {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// buildIndexedFilterQuery builds a case-sensitive, index-friendly SQL query on
// workspace_preferences("key" PRIMARY KEY) from exact keys and key prefixes.
// Overlapping keys/prefixes are collapsed so all UNION ALL branches are disjoint.
func buildIndexedFilterQuery(exactKeys, prefixes []string) (string, []any) {
	rawPrefixes := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p != "" {
			rawPrefixes = append(rawPrefixes, p)
		}
	}
	sort.Slice(rawPrefixes, func(i, j int) bool {
		if len(rawPrefixes[i]) != len(rawPrefixes[j]) {
			return len(rawPrefixes[i]) < len(rawPrefixes[j])
		}
		return rawPrefixes[i] < rawPrefixes[j]
	})
	keptPrefixes := make([]string, 0, len(rawPrefixes))
	for _, p := range rawPrefixes {
		covered := false
		for _, kept := range keptPrefixes {
			if strings.HasPrefix(p, kept) {
				covered = true
				break
			}
		}
		if !covered {
			keptPrefixes = append(keptPrefixes, p)
		}
	}

	seenKeys := make(map[string]bool, len(exactKeys))
	keptKeys := make([]string, 0, len(exactKeys))
	for _, k := range exactKeys {
		if k == "" || seenKeys[k] {
			continue
		}
		covered := false
		for _, p := range keptPrefixes {
			if strings.HasPrefix(k, p) {
				covered = true
				break
			}
		}
		if !covered {
			seenKeys[k] = true
			keptKeys = append(keptKeys, k)
		}
	}
	sort.Strings(keptKeys)

	if len(keptKeys) == 0 && len(keptPrefixes) == 0 {
		return "", nil
	}

	branches := make([]string, 0, 1+len(keptPrefixes))
	args := make([]any, 0, len(keptKeys)+2*len(keptPrefixes))

	if len(keptKeys) > 0 {
		placeholders := make([]string, len(keptKeys))
		for i, k := range keptKeys {
			placeholders[i] = "?"
			args = append(args, k)
		}
		branches = append(branches, `SELECT "key",value FROM workspace_preferences WHERE "key" IN (`+strings.Join(placeholders, ",")+`)`)
	}
	for _, p := range keptPrefixes {
		if upper, ok := prefixUpperBound(p); ok {
			branches = append(branches, `SELECT "key",value FROM workspace_preferences WHERE "key">=? AND "key"<?`)
			args = append(args, p, upper)
		} else {
			branches = append(branches, `SELECT "key",value FROM workspace_preferences WHERE "key">=?`)
			args = append(args, p)
		}
	}

	return strings.Join(branches, " UNION ALL ") + ` ORDER BY "key"`, args
}

func buildDefaultFilteredQuery(opts QueryOptions) (string, []any) {
	if opts.IncludeHeavyKeys {
		return `SELECT "key",value FROM workspace_preferences ORDER BY "key"`, nil
	}
	maxBytes := opts.MaxValueBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxValueBytes
	}

	conds := make([]string, 0, 2+len(DefaultExcludedPrefixes))
	args := make([]any, 0, len(DefaultExcludedKeys)+2*len(DefaultExcludedPrefixes)+1)

	if len(DefaultExcludedKeys) > 0 {
		placeholders := make([]string, len(DefaultExcludedKeys))
		for i, k := range DefaultExcludedKeys {
			placeholders[i] = "?"
			args = append(args, k)
		}
		conds = append(conds, `"key" NOT IN (`+strings.Join(placeholders, ",")+`)`)
	}
	for _, p := range DefaultExcludedPrefixes {
		if upper, ok := prefixUpperBound(p); ok {
			conds = append(conds, `NOT ("key">=? AND "key"<?)`)
			args = append(args, p, upper)
		} else {
			conds = append(conds, `NOT ("key">=?)`)
			args = append(args, p)
		}
	}
	conds = append(conds, `coalesce(length(value),0)<=?`)
	args = append(args, maxBytes)

	return `SELECT "key",value FROM workspace_preferences WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY "key"`, args
}

func decodePreferenceJSON(raw sql.NullString) (any, error) {
	if !raw.Valid {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw.String))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("stored preference must contain exactly one JSON value")
	}
	return value, nil
}

func readPreferences(ctx context.Context, db preferenceQuery) (State, error) {
	result := emptyState()
	query, args := buildIndexedFilterQuery(p02ExactKeys, p02Prefixes)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw sql.NullString
		if err := rows.Scan(&key, &raw); err != nil {
			return State{}, err
		}
		value, err := decodePreferenceJSON(raw)
		if err != nil {
			return State{}, err
		}
		category, suffix, _ := strings.Cut(key, ":")
		switch category {
		case "appearance":
			if value != nil {
				result.Appearance[suffix] = value
			}
		case "session_pins":
			if value != nil {
				result.SessionPins[suffix] = value
			}
		case "pinned_projects":
			result.PinnedProjects = value
		case "project_order":
			result.ProjectOrder = value
		}
	}
	return result, rows.Err()
}

func readKey(ctx context.Context, db preferenceQuery, key string) (any, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT "key",value FROM workspace_preferences WHERE "key"=?`, key)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	var foundKey string
	var raw sql.NullString
	if err := rows.Scan(&foundKey, &raw); err != nil {
		return nil, false, err
	}
	value, err := decodePreferenceJSON(raw)
	if err != nil {
		return nil, false, err
	}
	return value, true, rows.Err()
}

func queryPreferences(ctx context.Context, db preferenceQuery, opts QueryOptions) (map[string]any, error) {
	var query string
	var args []any
	if len(opts.Keys) > 0 || len(opts.Prefixes) > 0 {
		query, args = buildIndexedFilterQuery(opts.Keys, opts.Prefixes)
		if query == "" {
			return map[string]any{}, nil
		}
	} else {
		query, args = buildDefaultFilteredQuery(opts)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]any{}
	for rows.Next() {
		var key string
		var raw sql.NullString
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		value, err := decodePreferenceJSON(raw)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

// Apply validates and overwrites supplied keys while preserving unsupplied values.
func Apply(ctx context.Context, st *store.Store, patch Patch) (State, error) {
	return write(ctx, st, patch, false)
}

// Import validates supplied values but never overwrites any existing key or tombstone.
// Empty imported lists are ignored so an empty browser cannot initialize away server state.
func Import(ctx context.Context, st *store.Store, patch Patch) (State, error) {
	return write(ctx, st, patch, true)
}

func write(ctx context.Context, st *store.Store, patch Patch, importing bool) (State, error) {
	values, err := validatePatch(patch, importing)
	if err != nil {
		return State{}, err
	}
	var result State
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			raw, err := jsonValue(values[key])
			if err != nil {
				return err
			}
			if importing {
				_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO NOTHING`, key, raw)
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, key, raw)
			}
			if err != nil {
				return err
			}
		}
		result, err = readPreferences(ctx, tx)
		return err
	})
	if err != nil {
		return State{}, err
	}
	return result, nil
}

func validatePatch(patch Patch, importing bool) (map[string]any, error) {
	if len(patch.Appearance) > 10000 || len(patch.SessionPins) > 10000 {
		return nil, errors.New("preference map exceeds 10000 entries")
	}
	values := make(map[string]any, len(patch.Appearance)+len(patch.SessionPins)+2)
	for key, value := range patch.Appearance {
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("appearance: %w", err)
		}
		if value != nil {
			object, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("appearance %q must be an object or null", key)
			}
			for field := range object {
				if field != "color" && field != "icon" {
					return nil, fmt.Errorf("appearance %q allows only color and icon", key)
				}
			}
			color := ""
			if supplied, present := object["color"]; present {
				var colorOK bool
				color, colorOK = supplied.(string)
				if !colorOK {
					return nil, fmt.Errorf("appearance %q has invalid color", key)
				}
			}
			icon := "folder"
			if supplied, present := object["icon"]; present {
				var iconOK bool
				icon, iconOK = supplied.(string)
				if !iconOK {
					return nil, fmt.Errorf("appearance %q has invalid icon", key)
				}
			}
			if bound, ok := artIconColors[icon]; ok {
				color = bound
			}
			if !colors[color] || !icons[icon] {
				return nil, fmt.Errorf("appearance %q has invalid color or icon", key)
			}
			value = map[string]any{"color": color, "icon": icon}
		}
		values["appearance:"+key] = value
	}
	for key, value := range patch.SessionPins {
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("session_pins: %w", err)
		}
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("session pin %q must be boolean", key)
		}
		values["session_pins:"+key] = value
	}
	for key, list := range map[string]*[]string{"pinned_projects": patch.PinnedProjects, "project_order": patch.ProjectOrder} {
		if list == nil {
			continue
		}
		if len(*list) > 10000 {
			return nil, fmt.Errorf("%s exceeds 10000 entries", key)
		}
		if importing && len(*list) == 0 {
			continue
		}
		deduped := make([]string, 0, len(*list))
		seen := make(map[string]bool, len(*list))
		for _, item := range *list {
			if err := validateKey(item); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			if !seen[item] {
				seen[item] = true
				deduped = append(deduped, item)
			}
		}
		values[key] = deduped
	}
	return values, nil
}

func validateKey(key string) error {
	if count := utf8.RuneCountInString(key); count < 1 || count > 1024 {
		return errors.New("key must be 1-1024 characters")
	}
	return nil
}

func emptyState() State {
	return State{Appearance: map[string]any{}, SessionPins: map[string]any{}, PinnedProjects: []any{}, ProjectOrder: []any{}}
}

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func jsonValue(value any) (string, error) {
	encoded, err := jsonMarshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

var jsonMarshal = func(value any) ([]byte, error) {
	return marshalJSON(value)
}

func marshalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
