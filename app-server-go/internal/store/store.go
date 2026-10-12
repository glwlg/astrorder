// Package store provides the versioned SQLite application data store.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is the schema version understood by this package.
const CurrentSchemaVersion = 3

// SchemaMode controls whether opening a store may migrate its schema.
type SchemaMode uint8

const (
	// SchemaMigrate is the single-writer schema migrator mode.
	SchemaMigrate SchemaMode = iota
	// SchemaValidateOnly refuses missing, old, or structurally invalid schemas.
	SchemaValidateOnly
)

// Options controls store opening and connection-pool behavior.
type Options struct {
	SchemaMode     SchemaMode
	MaxOpenConns   int
	EventRetention int
}

// Store owns the App SQLite connection pool and registered frame projections.
type Store struct {
	db             *sql.DB
	eventRetention int64
	projections    map[string]*Projector
	projectionMu   sync.RWMutex
}

// Open opens path in isolated schema-migrator mode.
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{SchemaMode: SchemaMigrate})
}

var schemaMigrationMu sync.Mutex

// OpenWithOptions opens path with explicit schema ownership semantics.
func OpenWithOptions(path string, options Options) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store path is required")
	}
	if options.SchemaMode != SchemaMigrate && options.SchemaMode != SchemaValidateOnly {
		return nil, fmt.Errorf("unknown schema mode %d", options.SchemaMode)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve store path: %w", err)
	}
	if options.SchemaMode == SchemaMigrate {
		schemaMigrationMu.Lock()
		defer schemaMigrationMu.Unlock()
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return nil, fmt.Errorf("create store directory: %w", err)
		}
		migrationLock, err := acquireSchemaMigrationLock(absolute)
		if err != nil {
			return nil, err
		}
		defer migrationLock.Close()
	}
	if options.SchemaMode == SchemaValidateOnly {
		if info, statErr := os.Stat(absolute); statErr != nil {
			return nil, fmt.Errorf("validate store: %w", statErr)
		} else if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("validate store: %s is not a regular file", absolute)
		}
	}

	db, err := sql.Open("sqlite", sqliteDSNWithMode(absolute, options.SchemaMode))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	maxOpen := options.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 8
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store: %w", err)
	}

	retention := options.EventRetention
	if retention <= 0 {
		retention = 1000
	}
	store := &Store{db: db, eventRetention: int64(retention), projections: make(map[string]*Projector)}
	if options.SchemaMode == SchemaValidateOnly {
		err = validateSchema(context.Background(), db)
	} else {
		err = migrateSchema(context.Background(), db)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) string {
	return sqliteDSNWithMode(path, SchemaMigrate)
}

func sqliteDSNWithMode(path string, mode SchemaMode) string {
	fileURL := &url.URL{Scheme: "file", Opaque: filepath.ToSlash(path)}
	query := fileURL.Query()
	if mode == SchemaValidateOnly {
		query.Set("mode", "rw")
	} else {
		query.Set("mode", "rwc")
	}
	query.Add("_pragma", "busy_timeout(30000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Set("_txlock", "immediate")
	fileURL.RawQuery = query.Encode()
	return fileURL.String()
}

// Close closes the application database pool.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying pool for integration code that needs typed SQL.
func (s *Store) DB() *sql.DB { return s.db }

// WithTx runs fn in one transaction and rolls back on any returned error.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// QueryRows executes a bound-parameter query and returns decoded JSON columns.
func (s *Store) QueryRows(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return queryRows(ctx, s.db, query, args...)
}

// QueryRow executes a bound-parameter query and returns nil when no row exists.
func (s *Store) QueryRow(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// Projector is an opaque registration owned by exactly one Store. Keeping the
// callback here retains its closure without relying on function memory layout.
type Projector struct {
	owner *Store
	name  string
	apply ProjectionFunc
}

// RegisterProjector binds a unique name to its transaction callback and returns
// the only handle accepted by ApplyFrame. Callers cannot forge registrations.
func (s *Store) RegisterProjector(name string, fn ProjectionFunc) (*Projector, error) {
	if strings.TrimSpace(name) == "" || fn == nil {
		return nil, errors.New("projection name and callback are required")
	}
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	if _, exists := s.projections[name]; exists {
		return nil, fmt.Errorf("projection name already registered: %q", name)
	}
	projector := &Projector{owner: s, name: name, apply: fn}
	s.projections[name] = projector
	return projector, nil
}

func (s *Store) projectionRegistered(projector *Projector) bool {
	if projector == nil || projector.owner != s || projector.apply == nil {
		return false
	}
	s.projectionMu.RLock()
	defer s.projectionMu.RUnlock()
	return s.projections[projector.name] == projector
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryRows(ctx context.Context, db queryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			value := values[i]
			if raw, ok := value.([]byte); ok {
				value = string(raw)
			}
			if isJSONColumn(column) && value != nil {
				decoded, decodeErr := decodeJSON(value)
				if decodeErr != nil {
					return nil, fmt.Errorf("decode JSON column %s: %w", column, decodeErr)
				}
				value = decoded
			}
			row[column] = value
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
