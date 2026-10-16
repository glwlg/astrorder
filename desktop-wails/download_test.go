package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveDownloadStreamsAuthenticatedFile(t *testing.T) {
	body := []byte{0, 1, 255, 10}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/raw" || r.URL.Query().Get("connection_id") != "ssh-test" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("unexpected download request")
		}
		w.Write(body)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "下载 file.bin")
	if err := saveBackendFile(context.Background(), server.URL, "fixture", "/api/v1/files/raw?path=%2Ftmp%2Ffile&connection_id=ssh-test", target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(body) {
		t.Fatalf("saved %v: %v", got, err)
	}
}
func TestSaveDownloadRejectsArbitraryURLAndPreservesDestination(t *testing.T) {
	target := filepath.Join(t.TempDir(), "file.bin")
	os.WriteFile(target, []byte("original"), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "missing", 404) }))
	defer server.Close()
	for _, address := range []string{"https://evil.example/api/v1/files/raw", "//evil.example/api/v1/files/raw", "/api/v1/files/raw/../other", "/api/v1/files/raw?path=missing"} {
		if err := saveBackendFile(context.Background(), server.URL, "fixture", address, target); err == nil {
			t.Fatalf("accepted %s", address)
		}
		got, _ := os.ReadFile(target)
		if string(got) != "original" {
			t.Fatalf("overwrote destination on failure")
		}
	}
}
