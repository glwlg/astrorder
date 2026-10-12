package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSchemaRejectsStructuralDrift(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(kind, name, statement string) string
	}{
		{
			name: "wrong required default",
			mutate: func(kind, name, statement string) string {
				if kind == "table" && name == "sessions" {
					return strings.Replace(statement, "ephemeral BOOLEAN DEFAULT '0' NOT NULL", "ephemeral BOOLEAN DEFAULT '1' NOT NULL", 1)
				}
				return statement
			},
		},
		{
			name: "nullable required column",
			mutate: func(kind, name, statement string) string {
				if kind == "table" && name == "commands" {
					return strings.Replace(statement, "payload_hash VARCHAR(128) NOT NULL", "payload_hash VARCHAR(128)", 1)
				}
				return statement
			},
		},
		{
			name: "wrong primary key",
			mutate: func(kind, name, statement string) string {
				if kind == "table" && name == "workspace_preferences" {
					return strings.Replace(statement, `PRIMARY KEY ("key")`, `UNIQUE ("key")`, 1)
				}
				return statement
			},
		},
		{
			name: "wrong composite identity",
			mutate: func(kind, name, statement string) string {
				if kind == "table" && name == "sessions" {
					return strings.Replace(statement, "CONSTRAINT uq_sessions_agent_id UNIQUE (agent_id, id)", "CONSTRAINT uq_sessions_agent_id UNIQUE (id)", 1)
				}
				return statement
			},
		},
		{
			name: "named index points at wrong column",
			mutate: func(kind, name, statement string) string {
				if kind == "index" && name == "ix_sessions_agent_id" {
					return strings.Replace(statement, "(agent_id)", "(status)", 1)
				}
				return statement
			},
		},
	}

	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "drift.sqlite3")
			createSchemaFixture(t, path, mutation.mutate)
			st, err := OpenWithOptions(path, Options{SchemaMode: SchemaValidateOnly})
			if st != nil {
				_ = st.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "schema validation") {
				t.Fatalf("validate-only accepted structural drift: %v", err)
			}
		})
	}
}

func TestSchemaMigrationOwnershipIsCrossProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_STORE_LOCK_HELPER") == "1" {
		lock, err := acquireSchemaMigrationLock(os.Getenv("ASTRORDER_STORE_LOCK_PATH"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("locked")
		_, _ = os.Stdin.Read(make([]byte, 1))
		if err := lock.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "owned.sqlite3")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSchemaMigrationOwnershipIsCrossProcess$")
	command.Env = append(os.Environ(), "ASTRORDER_STORE_LOCK_HELPER=1", "ASTRORDER_STORE_LOCK_PATH="+path)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = stdin.Close()
		_ = command.Wait()
		t.Fatalf("migration lock helper did not become ready: line=%q err=%v stderr=%s", line, err, stderr.String())
	}
	if st, err := Open(path); err == nil || !strings.Contains(err.Error(), "schema migration is owned by another process") {
		if st != nil {
			_ = st.Close()
		}
		_ = stdin.Close()
		_ = command.Wait()
		t.Fatalf("second process entered migration: %v", err)
	}
	if _, err := stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("migration lock helper failed: %v stderr=%s", err, stderr.String())
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("migration lock was not released: %v", err)
	}
	_ = st.Close()
}

func createSchemaFixture(t *testing.T, path string, mutate func(kind, name, statement string) string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range tables {
		statement := mutate("table", table.Name, table.Create)
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create %s: %v", table.Name, err)
		}
	}
	for _, statement := range indices {
		name := strings.Fields(statement)[5]
		if _, err := db.Exec(mutate("index", name, statement)); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", CurrentSchemaVersion)); err != nil {
		t.Fatal(err)
	}
}
