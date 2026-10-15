package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	nativeprocess "astrorder.dev/session-daemon/internal/process"
	"github.com/coder/websocket"
)

// canonicalTransport owns only a viewer socket. Closing it never stops the
// profile's shared Hermes authority or its admitted work.
type canonicalTransport struct {
	conn   *websocket.Conn
	reader *io.PipeReader
	writer *io.PipeWriter
	cancel context.CancelFunc
	once   sync.Once
}

func dialCanonical(ctx context.Context, url string, protocols []string) (*canonicalTransport, error) {
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: protocols})
	if err != nil {
		return nil, fmt.Errorf("Hermes owner connection failed: %w", err)
	}
	if conn.Subprotocol() != "hermes-gateway-v1" {
		conn.CloseNow()
		return nil, fmt.Errorf("Hermes owner protocol mismatch")
	}
	conn.SetReadLimit(32 * 1024 * 1024)
	reader, writer := io.Pipe()
	lifetime, cancel := context.WithCancel(context.Background())
	t := &canonicalTransport{conn: conn, reader: reader, writer: writer, cancel: cancel}
	go func() {
		defer writer.Close()
		for {
			_, data, err := conn.Read(lifetime)
			if err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			if _, err = writer.Write(append(data, '\n')); err != nil {
				return
			}
		}
	}()
	return t, nil
}
func (t *canonicalTransport) Read(p []byte) (int, error) { return t.reader.Read(p) }
func (t *canonicalTransport) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.conn.Write(ctx, websocket.MessageText, bytes.TrimSpace(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (t *canonicalTransport) Close() error {
	t.once.Do(func() { t.cancel(); _ = t.conn.CloseNow(); _ = t.reader.Close(); _ = t.writer.Close() })
	return nil
}

// Use the launcher's exact bootstrap and managed ABI for discovery. Credentials
// stay in the private pipe and are never persisted or included in errors.
func canonicalDiscoveryArguments(arguments []string) ([]string, bool) {
	result := append([]string(nil), arguments...)
	for i, arg := range result {
		marker := "runpy.run_module('tui_gateway.entry', run_name='__main__', alter_sys=True)"
		if !strings.Contains(arg, marker) {
			continue
		}
		discovery := "from hermes_constants import get_hermes_home; from hermes_cli.gateway_runtime import ensure_gateway_runtime; from hermes_cli.gateway_client import _session_ticket, gateway_ws_target; import json; home=get_hermes_home().resolve(); found=ensure_gateway_runtime(home, idle_exit=True); assert found.state == 'ready' and found.endpoint is not None, 'Hermes owner is not ready: '+str(found.reason_code); url, protocols=gateway_ws_target(found.endpoint, _session_ticket(home, found.endpoint)); print(json.dumps({'url':url,'protocols':protocols}))"
		result[i] = strings.Replace(arg, marker, discovery, 1)
		return result, true
	}
	return nil, false
}

func (a *Adapter) discoverCanonical(ctx context.Context, arguments []string) (*canonicalTransport, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, a.config.Executable, arguments...)
	cmd.Dir = a.config.Workspace
	cmd.Env = nativeprocess.NativeEnvironment(a.config.Environment)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Hermes shared-owner discovery failed: %w", err)
	}
	var target struct {
		URL       string   `json:"url"`
		Protocols []string `json:"protocols"`
	}
	if json.Unmarshal(bytes.TrimSpace(output), &target) != nil || target.URL == "" {
		return nil, fmt.Errorf("Hermes shared-owner discovery returned an invalid target")
	}
	return dialCanonical(ctx, target.URL, target.Protocols)
}
