package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
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
