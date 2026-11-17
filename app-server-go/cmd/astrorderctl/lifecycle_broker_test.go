package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestLifecycleBrokerReplyRequiresToken(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	done := make(chan error, 1)
	go func() {
		for _, sent := range []string{strings.Repeat("b", 64), token} {
			connection, err := net.Dial("tcp4", listener.Addr().String())
			if err != nil {
				done <- err
				return
			}
			err = json.NewEncoder(connection).Encode(lifecycleBrokerReply{
				Token: sent, Code: 0, Output: []byte(`{"ok":true}`),
			})
			connection.Close()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, body, err := waitLifecycleBrokerReply(ctx, listener, token)
	if err != nil || code != 0 || string(body) != `{"ok":true}` {
		t.Fatalf("unexpected reply: code=%d body=%s err=%v", code, body, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleBrokerRejectsRemoteReplyAddress(t *testing.T) {
	var output bytes.Buffer
	code := runLifecycleBroker([]string{
		"--target", "app", "--action", "start", "--root", t.TempDir(), "--state-dir", t.TempDir(),
		"--reply", "192.0.2.1:1234", "--token", strings.Repeat("a", 64),
	}, &output)
	if code == 0 || !strings.Contains(output.String(), "invalid independent lifecycle controller arguments") {
		t.Fatalf("remote reply address was accepted: code=%d output=%s", code, output.String())
	}
}
