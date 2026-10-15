package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"image"
	"image/png"
	"testing"
)

func TestPreviewImageValidation(t *testing.T) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes())
	if _, err := decodePreview(data); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://evil.test/a.png", "data:text/html;base64,PHNjcmlwdD4=", "data:image/png;base64,bad"} {
		if _, err := decodePreview(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestPreviewCannotCallDesktopCapabilities(t *testing.T) {
	d := &DesktopService{shell: &ShellService{}}
	ctx := context.WithValue(context.Background(), application.WindowKey, &application.WebviewWindow{})
	if _, err := d.Run(ctx, "daemon", "stop"); err == nil {
		t.Fatal("unauthorized window may stop daemon")
	}
	if _, err := (&PreviewService{}).Data(context.Background()); err == nil {
		t.Fatal("windowless preview access accepted")
	}
}
func TestKernelStartupOrderAndFailure(t *testing.T) {
	for _, fail := range []string{"", "daemon", "app"} {
		var calls []string
		err := startKernels(context.Background(), func(_ context.Context, target, action string, confirm bool) (map[string]any, error) {
			calls = append(calls, target)
			if action != "start" || confirm {
				t.Fatal("startup requested destructive action")
			}
			if target == fail {
				return nil, errors.New("fixture failure")
			}
			return map[string]any{"ok": true, "state": "running"}, nil
		})
		if (err != nil) != (fail != "") {
			t.Fatalf("failure=%s err=%v", fail, err)
		}
		if calls[0] != "daemon" || (fail == "daemon" && len(calls) != 1) || (fail != "daemon" && len(calls) != 2) {
			t.Fatalf("wrong startup order %v", calls)
		}
	}
}
