package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverPanicsRePanicsErrAbortHandlerWithoutLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := recoverPanics(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	var got any
	func() {
		defer func() { got = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/plugins/dbx/api/connection/liveness-events", nil))
	}()

	if got != http.ErrAbortHandler {
		t.Fatalf("expected http.ErrAbortHandler to be re-panicked, got %v", got)
	}
	if strings.Contains(buf.String(), "unhandled http panic") {
		t.Fatalf("expected ErrAbortHandler not to log unhandled http panic, got: %s", buf.String())
	}
}

func TestRecoverPanicsLogsRealPanicsAndReturns500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := recoverPanics(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "unhandled http panic") {
		t.Fatalf("expected unhandled http panic log, got: %s", buf.String())
	}
}
