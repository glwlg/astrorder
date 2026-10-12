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

var colors = set("", "red", "orange", "amber", "yellow", "lime", "green", "emerald", "teal", "cyan", "sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink", "rose", "slate")
var icons = set("folder", "rocket", "tools", "database", "cloud", "code", "package", "book", "bug", "flame", "zap", "star", "heart", "globe", "server", "terminal", "lightbulb", "shield", "target", "gear", "coffee")

// Read returns only the four public P02 namespaces; tombstone appearance values are omitted.
func Read(ctx context.Context, st *store.Store) (State, error) {
	return readPreferences(ctx, st.DB())
}

type preferenceQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readPreferences(ctx context.Context, db preferenceQuery) (State, error) {
	result := emptyState()
	rows, err := db.QueryContext(ctx, `SELECT "key",value FROM workspace_preferences ORDER BY "key"`)
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
		var value any
		if raw.Valid {
			decoder := json.NewDecoder(strings.NewReader(raw.String))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return State{}, err
			}
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				return State{}, errors.New("stored preference must contain exactly one JSON value")
			}
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
