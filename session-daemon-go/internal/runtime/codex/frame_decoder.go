package codex

import (
	"bytes"
	"encoding/json"
	"io"
)

const retainedDecoderLimit = 1 << 20

// Replace oversized decoder storage before blocking for the next frame. Copy
// unread lookahead first: Buffered otherwise references the old large buffer.
type frameDecoder struct {
	source  io.Reader
	decoder *json.Decoder
}

func newFrameDecoder(source io.Reader) *frameDecoder {
	d := &frameDecoder{source: source, decoder: json.NewDecoder(source)}
	d.decoder.UseNumber()
	return d
}
func (d *frameDecoder) Decode(frame *map[string]any) error {
	start := d.decoder.InputOffset()
	if err := d.decoder.Decode(frame); err != nil {
		return err
	}
	if d.decoder.InputOffset()-start >= retainedDecoderLimit {
		unread, err := io.ReadAll(d.decoder.Buffered())
		if err != nil {
			return err
		}
		d.source = io.MultiReader(bytes.NewReader(unread), d.source)
		d.decoder = json.NewDecoder(d.source)
		d.decoder.UseNumber()
	}
	return nil
}
