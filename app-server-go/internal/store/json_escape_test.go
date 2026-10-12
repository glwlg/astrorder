package store

import "testing"

func TestPayloadHashDistinguishesLiteralUnicodeEscapesFromSeparators(t *testing.T) {
	fixtures := []struct {
		text string
		hash string
	}{
		{`literal \u2028 \u2029`, "e646ca468a11f27bb0ff4fbc9a6af1b7e380ec07dc190b12c4e26da2739e7516"},
		{"actual \u2028 \u2029", "289630a9f66c568fc81c709ff755ef16a440c38050d9472c78aa33773fbe1460"},
		{`mixed \u2028`, "1ac59d8fe020be53131f488bab3ec70d3d405bbf4e2fe2795e830c41dcfaa0f8"},
	}
	// Constants were obtained from Python json.dumps with ensure_ascii=False,
	// sort_keys=True and compact separators on these exact synthetic strings.
	for _, fixture := range fixtures {
		t.Run(fixture.hash, func(t *testing.T) {
			hash, err := payloadHash(map[string]any{"text": fixture.text})
			if err != nil || hash != fixture.hash {
				t.Fatalf("literal escape semantics changed: hash=%s want=%s err=%v", hash, fixture.hash, err)
			}
		})
	}
}
