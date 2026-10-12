package attachments

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
)

type allowBrowser struct{}

func (allowBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

type memoryStore struct {
	rows map[string]map[string]any
	err  error
}

func (s *memoryStore) AddAttachment(_ context.Context, payload map[string]any) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	row := map[string]any{}
	for key, value := range payload {
		row[key] = value
	}
	s.rows[row["id"].(string)] = row
	return row, nil
}

func (s *memoryStore) GetAttachment(_ context.Context, id string) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows[id], nil
}

type eventRecorder struct{ names []string }

func (p *eventRecorder) Publish(_ context.Context, name string, _ map[string]any) error {
	p.names = append(p.names, name)
	return nil
}

func TestRegistrationsExposeAttachmentRoutes(t *testing.T) {
	registrations := Registrations()
	if len(registrations) != 2 || registrations[0].Path != "/api/v1/attachments" || registrations[1].Path != "/api/v1/attachments/{attachment_id}" {
		t.Fatalf("registrations=%#v", registrations)
	}
	for _, registration := range registrations {
		if registration.Kind != "http" || registration.Auth != "browser" || registration.Owner != "go-app" {
			t.Fatalf("registration=%#v", registration)
		}
	}
}

func TestAttachmentHTTPStreamsUploadAndDownload(t *testing.T) {
	root := t.TempDir()
	store := &memoryStore{rows: make(map[string]map[string]any)}
	publisher := &eventRecorder{}
	handler, err := NewHandler(Options{
		Authorizer: allowBrowser{}, Store: store, Publisher: publisher, Root: root,
		MaxSize: 1024, AllowedTypes: []string{"image/png", "text/plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x89, 'P', 'N', 'G', 0, 0xff, '\n'}
	request := multipartRequest(t, "报告 图.png", "image/png", payload, "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload=%d %s", response.Code, response.Body.String())
	}
	var record map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	id := record["id"].(string)
	stored := store.rows[id]
	if stored == nil || stored["name"] != "报告 图.png" || stored["source_path"] != nil {
		t.Fatalf("stored=%#v", stored)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".") {
		t.Fatalf("files=%v err=%v", entries, err)
	}

	download := httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+id, nil)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != 200 || !strings.Contains(downloadResponse.Header().Get("Content-Disposition"), "filename*=") || string(downloadResponse.Body.Bytes()) != string(payload) {
		t.Fatalf("download=%d headers=%v body=%v", downloadResponse.Code, downloadResponse.Header(), downloadResponse.Body.Bytes())
	}
	if len(publisher.names) != 1 || publisher.names[0] != "attachment.created" {
		t.Fatalf("events=%v", publisher.names)
	}

	metadataHandler, err := NewHandler(Options{Authorizer: allowBrowser{}, Store: store, Root: root, MaxSize: 1024, AllowedTypes: []string{"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	metadataResponse := httptest.NewRecorder()
	metadataHandler.ServeHTTP(metadataResponse, multipartRequest(t, "note.txt", "text/plain", []byte("note"), `C:\private\note.txt`))
	if metadataResponse.Code != 201 {
		t.Fatalf("metadata upload=%d %s", metadataResponse.Code, metadataResponse.Body.String())
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataResponse.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if store.rows[metadata["id"].(string)]["source_path"] != `C:\private\note.txt` {
		t.Fatalf("source metadata=%#v", store.rows[metadata["id"].(string)])
	}
}

func TestAttachmentHTTPRejectsSourcePathOversizeAndTypeAndCleansFiles(t *testing.T) {
	root := t.TempDir()
	store := &memoryStore{rows: make(map[string]map[string]any)}
	handler, err := NewHandler(Options{Authorizer: allowBrowser{}, Store: store, Root: root, MaxSize: 4, AllowedTypes: []string{"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name       string
		filename   string
		mediaType  string
		payload    []byte
		sourcePath string
		status     int
	}{
		{name: "source path invalid", filename: "a.txt", mediaType: "text/plain", payload: []byte("x"), sourcePath: `C:\secret\b.txt`, status: 400},
		{name: "oversize", filename: "a.txt", mediaType: "text/plain", payload: []byte("12345"), status: 413},
		{name: "type", filename: "a.png", mediaType: "image/png", payload: []byte("x"), status: 415},
	}
	if _, err := safeName("../a.txt"); err == nil {
		t.Fatal("path filename was accepted")
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, multipartRequest(t, check.filename, check.mediaType, check.payload, check.sourcePath))
			if response.Code != check.status || !strings.Contains(response.Body.String(), `"detail"`) {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed uploads left files=%v err=%v", entries, err)
	}
}

func TestAttachmentSQLFailureRollsBackPublishedBlobAndDownloadRejectsBadStorage(t *testing.T) {
	root := t.TempDir()
	store := &memoryStore{rows: make(map[string]map[string]any), err: io.ErrUnexpectedEOF}
	handler, err := NewHandler(Options{Authorizer: allowBrowser{}, Store: store, Root: root, MaxSize: 100, AllowedTypes: []string{"text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, multipartRequest(t, "a.txt", "text/plain", []byte("data"), ""))
	if response.Code != 500 {
		t.Fatalf("upload=%d %s", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rollback files=%v err=%v", entries, err)
	}

	store.err = nil
	store.rows["bad"] = map[string]any{"id": "bad", "name": "bad", "media_type": "text/plain", "storage_name": "../outside"}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/attachments/bad", nil))
	if response.Code != 404 {
		t.Fatalf("bad storage=%d %s", response.Code, response.Body.String())
	}
}

func multipartRequest(t *testing.T, filename, mediaType string, payload []byte, sourcePath string) *http.Request {
	t.Helper()
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	if sourcePath != "" {
		if err := writer.WriteField("source_path", sourcePath); err != nil {
			t.Fatal(err)
		}
	}
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	header["Content-Type"] = []string{mediaType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/attachments", strings.NewReader(body.String()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

var _ = filepath.Separator
