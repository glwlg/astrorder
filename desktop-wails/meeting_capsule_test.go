package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestMeetingCapsuleSanitizeState(t *testing.T) {
	raw := MeetingCapsuleState{
		Active:  true,
		Elapsed: "00:12:34:56:78:90:123",
		Text:    "  " + strings.Repeat("字", 220) + "\n换行  ",
		Bars:    []float64{-1, 0.05, 0.5, 2.5, 0.8, 0.3, 0.4, 0.5, 0.9},
	}
	clean := sanitizeCapsuleState(raw)
	if len([]rune(clean.Elapsed)) > 16 {
		t.Fatalf("elapsed not truncated: %q", clean.Elapsed)
	}
	if len([]rune(clean.Text)) != 180 {
		t.Fatalf("text rune length = %d, want 180", len([]rune(clean.Text)))
	}
	if len(clean.Bars) != 8 {
		t.Fatalf("bars length = %d, want 8", len(clean.Bars))
	}
	if clean.Bars[0] != 0.1 || clean.Bars[3] != 1 {
		t.Fatalf("bars not clamped: %v", clean.Bars)
	}
}

func TestMeetingCapsuleWindowAuthorization(t *testing.T) {
	svc := &MeetingCapsuleService{shell: &ShellService{}}
	if _, err := svc.State(context.Background()); err == nil {
		t.Fatal("windowless State access accepted")
	}
	if err := svc.Stop(context.Background()); err == nil {
		t.Fatal("windowless Stop access accepted")
	}
	if err := svc.TogglePause(context.Background()); err == nil {
		t.Fatal("windowless TogglePause access accepted")
	}
	if err := svc.Expand(context.Background()); err == nil {
		t.Fatal("windowless Expand access accepted")
	}

	d := &DesktopService{shell: &ShellService{meetingCapsule: svc}}
	unauthCtx := context.WithValue(context.Background(), application.WindowKey, &application.WebviewWindow{})
	if err := d.ShowMeetingCapsule(unauthCtx, MeetingCapsuleState{Active: true}); err == nil {
		t.Fatal("unauthorized window allowed to show meeting capsule")
	}
	if err := d.UpdateMeetingCapsule(unauthCtx, MeetingCapsuleState{Active: true}); err == nil {
		t.Fatal("unauthorized window allowed to update meeting capsule")
	}
	if err := d.HideMeetingCapsule(unauthCtx); err == nil {
		t.Fatal("unauthorized window allowed to hide meeting capsule")
	}
}

func TestAssetsServeMeetingCapsuleAndBridge(t *testing.T) {
	handler, err := assetHandler(Config{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/meeting-capsule.html", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "meetingcapsuleservice.js") {
		t.Fatalf("capsule html response = %d, body missing binding import", w.Code)
	}

	bw := httptest.NewRecorder()
	handler.ServeHTTP(bw, httptest.NewRequest(http.MethodGet, bridgePath, nil))
	script := bw.Body.String()
	for _, fn := range []string{"showMeetingCapsule", "updateMeetingCapsule", "hideMeetingCapsule"} {
		if !strings.Contains(script, fn) {
			t.Fatalf("desktop bridge missing %q", fn)
		}
	}
}
