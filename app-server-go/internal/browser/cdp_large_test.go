package browser

import (
	"context"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCDPLargeScreenshotKeepsConnectionAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for i := 1; i <= 2; i++ {
			_, _, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			payload := `{"id":1,"result":{"data":"` + strings.Repeat("A", 128*1024) + `"}}`
			if i == 2 {
				payload = `{"id":2,"result":{}}`
			}
			if ws.Write(r.Context(), websocket.MessageText, []byte(payload)) != nil {
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialCDP(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Call(ctx, "Page.captureScreenshot", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Call(ctx, "Runtime.evaluate", nil); err != nil {
		t.Fatal(err)
	}
}
