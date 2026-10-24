package codex

import (
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"testing"
)

func TestFrameDecoderReleasesLargeStoragePreservesLookahead(t *testing.T) {
	large := `{"text":"` + strings.Repeat("x", 8<<20) + `","id":9007199254740993}`
	d := newFrameDecoder(strings.NewReader(large + large + `{"id":3}`))
	for i := 0; i < 2; i++ {
		old := d.decoder
		var f map[string]any
		if err := d.Decode(&f); err != nil {
			t.Fatal(err)
		}
		if f["id"] != json.Number("9007199254740993") {
			t.Fatal("integer changed")
		}
		if d.decoder == old {
			t.Fatal("oversized decoder retained")
		}
	}
	var f map[string]any
	if err := d.Decode(&f); err != nil || f["id"] != json.Number("3") {
		t.Fatal("lookahead lost", f, err)
	}
	if err := d.Decode(&f); err != io.EOF {
		t.Fatal(err)
	}
}
func TestFrameDecoderLiveHeapAfterLargeFrame(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		io.WriteString(writer, `{"text":"`)
		chunk := strings.Repeat("x", 65536)
		for i := 0; i < 512; i++ {
			io.WriteString(writer, chunk)
		}
		io.WriteString(writer, `"}`)
	}()
	d := newFrameDecoder(reader)
	func() {
		var f map[string]any
		if err := d.Decode(&f); err != nil {
			t.Fatal(err)
		}
	}()
	runtime.GC()
	runtime.ReadMemStats(&after)
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("live heap growth after 32 MiB frame: %d bytes", delta)
	if delta > 8<<20 {
		t.Fatalf("large decoder retained: %d", delta)
	}
	runtime.KeepAlive(d)
}
