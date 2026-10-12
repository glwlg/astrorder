package attachments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type failedPublisher struct{}

func (failedPublisher) Publish(context.Context, string, map[string]any) error {
	return errors.New("event persistence unavailable")
}

func TestUploadPublicationFailurePreservesCommittedBlob(t *testing.T) {
	root := t.TempDir()
	store := &memoryStore{rows: make(map[string]map[string]any)}
	handler, err := NewHandler(Options{Authorizer: allowBrowser{}, Store: store, Publisher: failedPublisher{}, Root: root, MaxSize: 1024, AllowedTypes: []string{"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	payload := []byte("committed attachment")
	handler.ServeHTTP(response, multipartRequest(t, "committed.txt", "text/plain", payload, ""))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("publication failure hidden: %d %s", response.Code, response.Body.String())
	}
	if len(store.rows) != 1 {
		t.Fatalf("expected committed row, got %d", len(store.rows))
	}
	for id, row := range store.rows {
		blob, err := os.ReadFile(filepath.Join(root, row["storage_name"].(string)))
		if err != nil || string(blob) != string(payload) {
			t.Fatalf("committed blob lost: err=%v", err)
		}
		download := httptest.NewRecorder()
		handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+id, nil))
		if download.Code != http.StatusOK || string(download.Body.Bytes()) != string(payload) {
			t.Fatalf("committed attachment unreadable: %d", download.Code)
		}
	}
}
