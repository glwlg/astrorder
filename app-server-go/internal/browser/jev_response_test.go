package browser

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type jevResponseTransport struct{ body string }

func (r jevResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(r.body)), Header: make(http.Header)}, nil
}
func TestJevRejectsTrailingDelimiterAndOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{"trailing_closing_brace": `{"answers":{}}}`, "oversized": `{"answers":{}}` + strings.Repeat(" ", 16000000)} {
		t.Run(name, func(t *testing.T) {
			p := &JevPlanner{JevKey: "fixture", Client: &http.Client{Transport: jevResponseTransport{body: body}}}
			if response, err := p.InvokeJevHTTP(context.Background(), map[string]any{}, map[string]any{}); err == nil {
				t.Fatalf("invalid response accepted: %v", response)
			}
		})
	}
}
