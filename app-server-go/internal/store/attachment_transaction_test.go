package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAttachmentReadbackFailureRollsBackInsert(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Force wire conversion to fail after the actual SQLite insert. The API
	// must not report failure while leaving a committed blob-owning row.
	_, err = s.DB().ExecContext(ctx, `CREATE TRIGGER invalid_attachment_timestamp
AFTER INSERT ON attachments BEGIN
UPDATE attachments SET created_at='invalid-date' WHERE id=NEW.id;
END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddAttachment(ctx, map[string]any{
		"id": "readback-failure", "name": "fixture.txt", "media_type": "text/plain",
		"size": 7, "storage_name": "fixture.blob",
	})
	if err == nil {
		t.Fatal("invalid readback timestamp was accepted")
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM attachments WHERE id=?", "readback-failure").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed insertion retained committed attachment metadata: count=%d", count)
	}
}
