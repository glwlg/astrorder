package files

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileUploadHTTPAndDeduplication(t *testing.T) {
	root := t.TempDir()
	publisher := &eventRecorder{}
	handler := testHandler(root, fakeStore{session: map[string]any{"workspace": root, "connection_id": "local"}}, publisher, nil)

	uploadOnce := func(filename, content string) (int, map[string]any) {
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		_ = mw.WriteField("dir", root)
		_ = mw.WriteField("session_id", "s")
		_ = mw.WriteField("connection_id", "local")
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(content))
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code1, out1 := uploadOnce("会议记录.md", "# 第一版")
	if code1 != http.StatusOK {
		t.Fatalf("first upload failed: %d %+v", code1, out1)
	}
	if out1["name"] != "会议记录.md" {
		t.Fatalf("unexpected first name: %+v", out1)
	}

	code2, out2 := uploadOnce("会议记录.md", "# 第二版")
	if code2 != http.StatusOK {
		t.Fatalf("second upload failed: %d %+v", code2, out2)
	}
	if out2["name"] != "会议记录 (1).md" {
		t.Fatalf("expected deduplicated name '会议记录 (1).md', got %+v", out2)
	}

	data1, err := os.ReadFile(filepath.Join(root, "会议记录.md"))
	if err != nil || string(data1) != "# 第一版" {
		t.Fatalf("first file mismatch: %q %v", data1, err)
	}
	data2, err := os.ReadFile(filepath.Join(root, "会议记录 (1).md"))
	if err != nil || string(data2) != "# 第二版" {
		t.Fatalf("second file mismatch: %q %v", data2, err)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("expected 2 files.changed events, got %d", len(publisher.events))
	}
}
