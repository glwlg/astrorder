package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"strings"
	"testing"
)

func TestKernelMenuState(t *testing.T) {
	for _, tc := range []struct {
		state       map[string]any
		label       string
		start, stop bool
	}{
		{nil, "大内核 · 状态未知", false, false},
		{map[string]any{"state": "running", "ok": true, "port": 30102}, "大内核 · 运行中", false, true},
		{map[string]any{"state": "stopped", "ok": true}, "大内核 · 已停止", true, false},
		{map[string]any{"state": "conflict", "ok": true}, "大内核 · 状态冲突", false, false},
		{map[string]any{"state": "running", "ok": false}, "大内核 · 状态未知", false, false},
		{map[string]any{"state": "unrecognised"}, "大内核 · 状态未知", false, false},
	} {
		got := describeKernel("大内核", tc.state)
		if got.Label != tc.label || got.CanStart != tc.start || got.CanStop != tc.stop || got.CanRestart != tc.stop {
			t.Fatalf("%+v => %+v", tc, got)
		}
	}
}

func TestTrayIconsHaveExactSmallSizesAndTransparency(t *testing.T) {
	for _, data := range [][]byte{trayIcon, trayDarkIcon} {
		if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
			t.Fatal("invalid ICO")
		}
		n := int(binary.LittleEndian.Uint16(data[4:6]))
		seen := map[int]bool{}
		for i := 0; i < n; i++ {
			entry := data[6+i*16 : 6+(i+1)*16]
			size := int(entry[0])
			length := int(binary.LittleEndian.Uint32(entry[8:12]))
			offset := int(binary.LittleEndian.Uint32(entry[12:16]))
			img, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
				t.Fatal("frame size mismatch")
			}
			_, _, _, alpha := img.At(0, 0).RGBA()
			if alpha != 0 {
				t.Fatal("tray icon background must be transparent")
			}
			seen[size] = true
		}
		for _, size := range []int{16, 20, 24, 32, 40, 48} {
			if !seen[size] {
				t.Fatalf("missing tray DPI frame %d", size)
			}
		}
	}
}

func TestTrayTooltipUsesFriendlyNonJargonCopy(t *testing.T) {
	running := map[string]any{"state": "running", "ok": true, "port": 30102}
	stopped := map[string]any{"state": "stopped", "ok": true}
	starting := map[string]any{"state": "starting", "ok": true}
	conflict := map[string]any{"state": "conflict", "ok": true}

	if got := describeTrayTooltip(running, running); got != "星序 · 运行正常" {
		t.Fatalf("normal tooltip should be friendly without jargon, got %q", got)
	}
	if got := describeTrayTooltip(running, stopped); !strings.Contains(got, "界面已就绪") {
		t.Fatalf("degraded tooltip unexpected: %q", got)
	}
	if got := describeTrayTooltip(starting, running); !strings.Contains(got, "正在启动") {
		t.Fatalf("starting tooltip unexpected: %q", got)
	}
	if got := describeTrayTooltip(stopped, running); !strings.Contains(got, "后台任务持续运行") {
		t.Fatalf("background daemon tooltip unexpected: %q", got)
	}
	if got := describeTrayTooltip(conflict, running); strings.Contains(got, "大内核") || strings.Contains(got, "状态冲突") {
		t.Fatalf("conflict tooltip should avoid scary jargon, got %q", got)
	}
}

func TestTrayPreservesSmallKernelUpdateMenuContract(t *testing.T) {
	if trayDaemonServiceLabel != "小内核" {
		t.Fatalf("AGENTS.md requires preserving 小内核 submenu label, got %q", trayDaemonServiceLabel)
	}
	if trayDaemonUpdateLabel != "更新小内核" {
		t.Fatalf("AGENTS.md requires preserving 更新小内核 menu action, got %q", trayDaemonUpdateLabel)
	}
	if trayAppServiceLabel != "主服务" {
		t.Fatalf("expected user-friendly 主服务 label for app service, got %q", trayAppServiceLabel)
	}
}

func TestDescribeKernelFriendlyDetails(t *testing.T) {
	gotRunning := describeKernel("主服务", map[string]any{"state": "running", "ok": true, "port": 30102})
	if !strings.Contains(gotRunning.Detail, "正常工作中") || !strings.Contains(gotRunning.Detail, "30102") {
		t.Fatalf("unexpected running detail: %q", gotRunning.Detail)
	}
	gotConflict := describeKernel("主服务", map[string]any{"state": "conflict", "ok": true})
	if !strings.Contains(gotConflict.Detail, "打开设置") {
		t.Fatalf("expected actionable conflict detail, got %q", gotConflict.Detail)
	}
}
