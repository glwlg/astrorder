package settingsapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type jevTransport func(*http.Request) (*http.Response, error)

func (f jevTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSystemOneConnectionUsesNativeHTTP(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	calls := 0
	h.HTTPClient = &http.Client{Transport: jevTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.String() != "https://api.typesafe.ai/v1/systemone" || body["model"] != "jev-latest" {
			t.Fatal("invalid System One request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":{"recommended_component":{"choice":"MetricGrid","confidence":0.9}}}`)), Header: http.Header{}}, nil
	})}
	call(h, "POST", "/api/v1/services/jev/config", `{"api_key":"fixture"}`)
	probe := call(h, "POST", "/api/v1/services/jev/test", `{}`)
	if probe.Code != 200 || !strings.Contains(probe.Body.String(), `"component":"MetricGrid"`) {
		t.Fatal(probe.Code, probe.Body.String())
	}
	if calls != 1 {
		t.Fatalf("upstream calls %d", calls)
	}
}
