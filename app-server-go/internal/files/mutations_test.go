package files

import (
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestFileRenameAndDeleteHTTP(t *testing.T) {
 root := t.TempDir()
 old := filepath.Join(root, "旧 文件.mod")
 if err := os.WriteFile(old, []byte("module example"), 0600); err != nil { t.Fatal(err) }
 publisher := &eventRecorder{}
 handler := testHandler(root, fakeStore{session: map[string]any{"workspace": root, "connection_id": "local"}}, publisher, nil)
 payload := `{"path":`+quote(old)+`,"name":"新 文件.mod","session_id":"s","connection_id":"local"}`
 response := call(t, handler, http.MethodPost, "/api/v1/files/rename", strings.NewReader(payload))
 if response.Code != 200 { t.Fatalf("rename: %d %s", response.Code, response.Body.String()) }
 renamed := filepath.Join(root, "新 文件.mod")
 if bytes, err := os.ReadFile(renamed); err != nil || string(bytes) != "module example" { t.Fatalf("renamed content: %q %v", bytes, err) }
 if _, err := os.Stat(old); !os.IsNotExist(err) { t.Fatalf("source remains: %v", err) }
 response = call(t, handler, http.MethodPost, "/api/v1/files/delete", strings.NewReader(`{"path":`+quote(renamed)+`,"session_id":"s","connection_id":"local"}`))
 if response.Code != 200 { t.Fatalf("delete: %d %s", response.Code, response.Body.String()) }
 if _, err := os.Stat(renamed); !os.IsNotExist(err) { t.Fatalf("deleted file remains: %v", err) }
 if len(publisher.events) != 2 { t.Fatalf("events: %v", publisher.events) }
}

func TestFileMutationsRejectUnsafeTargets(t *testing.T) {
 root := t.TempDir()
 file := filepath.Join(root, "source.txt")
 other := filepath.Join(root, "other.txt")
 for _, path := range []string{file, other} { if err := os.WriteFile(path, []byte(path), 0600); err != nil { t.Fatal(err) } }
 outside := filepath.Join(t.TempDir(), "outside.txt")
 if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil { t.Fatal(err) }
 handler := testHandler(root, fakeStore{}, nil, nil)
 for _, test := range []struct{ action, path, name string; status int }{
  {"rename", file, "other.txt", 409}, {"rename", file, "../escape.txt", 400},
  {"rename", file, "", 400}, {"delete", root, "", 400}, {"delete", outside, "", 403},
 } {
  payload := `{"path":`+quote(test.path)+`,"name":`+quote(test.name)+`}`
  response := call(t, handler, http.MethodPost, "/api/v1/files/"+test.action, strings.NewReader(payload))
  if response.Code != test.status { t.Errorf("%+v: %d %s", test, response.Code, response.Body.String()) }
 }
 for _, path := range []string{file, other, outside} { if bytes, err := os.ReadFile(path); err != nil || string(bytes) != map[string]string{file:file,other:other,outside:"outside"}[path] { t.Errorf("changed rejected target %s: %q %v", path,bytes,err) } }
 response := call(t, handler, http.MethodPost, "/api/v1/files/delete", strings.NewReader(`{"path":`+quote(file)+`,"connection_id":"missing"}`))
 if response.Code != 404 { t.Fatalf("remote fallback: %d %s", response.Code,response.Body.String()) }
 if _, err := os.Stat(file); err != nil { t.Fatal("unknown connection deleted local file",err) }
}
