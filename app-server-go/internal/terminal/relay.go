// Package terminal relays browser terminals to independently owned daemon PTYs.
package terminal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"github.com/coder/websocket"
	"google.golang.org/grpc"
)

// Relay owns browser attachments only. It never creates a local PTY process.
type Relay struct {
	Client           *daemonclient.Client
	Store            *store.Store
	Authorizer       *auth.Authenticator
	DefaultWorkspace string
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if failure := r.Authorizer.AuthorizeBrowser(request); failure != nil {
		http.Error(w, failure.Detail, failure.Status)
		return
	}
	open, err := r.resolve(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	after := int64(0)
	if raw := request.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			http.Error(w, "Invalid terminal cursor", http.StatusBadRequest)
			return
		}
	}
	conn, err := websocket.Accept(w, request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(int64(sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_TERMINAL_INPUT_BYTES))
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	unregister := r.Client.RegisterSubscription(cancel)
	defer unregister()
	stream, err := r.Client.Terminal.Open(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "Daemon terminal unavailable")
		return
	}
	streamID := open.Session.SessionId
	if err = stream.Send(&sessiondv1.TerminalClientFrame{TerminalStreamId: streamID, ReconnectAfterChunkSeq: after, Frame: &sessiondv1.TerminalClientFrame_Open{Open: open}}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "Daemon terminal open failed")
		return
	}
	first, err := stream.Recv()
	if err != nil || first.GetOpened() == nil || first.GetTerminalStreamId() != streamID {
		_ = conn.Close(websocket.StatusInternalError, "Daemon terminal open was not confirmed")
		return
	}
	done := make(chan error, 1)
	go func() {
		err := relayOutput(ctx, conn, stream, streamID)
		done <- err
		cancel()
	}()
	for {
		kind, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return
		}
		frame := &sessiondv1.TerminalClientFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalClientFrame_Input{Input: &sessiondv1.TerminalInput{Data: data}}}
		explicitClose := false
		if kind == websocket.MessageText && len(data) > 0 && data[0] == '{' {
			var control struct {
				Type string `json:"type"`
				Cols uint32 `json:"cols"`
				Rows uint32 `json:"rows"`
			}
			if json.Unmarshal(data, &control) == nil {
				switch control.Type {
				case "resize":
					frame.Frame = &sessiondv1.TerminalClientFrame_Resize{Resize: &sessiondv1.TerminalResize{Cols: control.Cols, Rows: control.Rows}}
				case "interrupt":
					frame.Frame = &sessiondv1.TerminalClientFrame_Interrupt{Interrupt: &sessiondv1.TerminalInterrupt{}}
				case "close":
					frame.Frame = &sessiondv1.TerminalClientFrame_Close{Close: &sessiondv1.TerminalClose{Reason: "Browser explicitly closed terminal"}}
					explicitClose = true
				}
			}
		}
		if err = stream.Send(frame); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "Daemon terminal input failed")
			return
		}
		if explicitClose {
			select {
			case <-done:
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
	}
}

func relayOutput(ctx context.Context, conn *websocket.Conn, stream grpc.BidiStreamingClient[sessiondv1.TerminalClientFrame, sessiondv1.TerminalServerFrame], id string) error {
	var carry []byte
	write := func(data []byte, final bool) error {
		carry = append(carry, data...)
		end := 0
		for end < len(carry) {
			if !utf8.FullRune(carry[end:]) && !final {
				break
			}
			_, size := utf8.DecodeRune(carry[end:])
			end += size
		}
		if end == 0 {
			return nil
		}
		text := strings.ToValidUTF8(string(carry[:end]), "\ufffd")
		carry = append(carry[:0], carry[end:]...)
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return conn.Write(writeCtx, websocket.MessageText, []byte(text))
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "Daemon terminal stream ended")
			return err
		}
		if frame.GetTerminalStreamId() != id {
			_ = conn.Close(websocket.StatusInternalError, "Terminal identity mismatch")
			return errors.New("terminal identity mismatch")
		}
		switch payload := frame.Frame.(type) {
		case *sessiondv1.TerminalServerFrame_Output:
			if err = write(payload.Output.Data, false); err != nil {
				return err
			}
		case *sessiondv1.TerminalServerFrame_Exit:
			if err = write(nil, true); err != nil {
				return err
			}
			return conn.Close(websocket.StatusNormalClosure, "Terminal exited")
		case *sessiondv1.TerminalServerFrame_Overflow:
			_ = conn.Close(websocket.StatusTryAgainLater, "Terminal requires replay")
			return errors.New("terminal requires replay")
		case *sessiondv1.TerminalServerFrame_Error:
			_ = conn.Close(websocket.StatusInternalError, "Daemon terminal failed")
			return errors.New("daemon terminal failed")
		default:
			_ = conn.Close(websocket.StatusInternalError, "Invalid terminal frame")
			return errors.New("invalid terminal frame")
		}
	}
}

func (r *Relay) resolve(request *http.Request) (*sessiondv1.OpenTerminal, error) {
	workspace := r.DefaultWorkspace
	agentID, sid, connectionID := "", request.URL.Query().Get("session_id"), ""
	if sid != "" {
		if r.Store == nil {
			return nil, errors.New("Session store is unavailable")
		}
		var session map[string]any
		var err error
		if agentID = request.URL.Query().Get("agent_id"); agentID != "" {
			session, err = r.Store.GetSession(request.Context(), agentID, sid)
		} else {
			all, listErr := r.Store.ListSessions(request.Context(), "")
			if listErr != nil {
				return nil, listErr
			}
			for _, item := range all {
				if item["id"] == sid {
					if session != nil {
						return nil, errors.New("Session is ambiguous; agent_id is required")
					}
					session = item
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if session == nil {
			return nil, errors.New("Session was not found")
		}
		agentID, _ = session["agent_id"].(string)
		if value, ok := session["workspace"].(string); ok && value != "" {
			workspace = value
		}
		connectionID, _ = session["connection_id"].(string)
	} else {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, err
		}
		sid = hex.EncodeToString(bytes[:])
		agentID = "terminal"
	}
	if agentID == "" || len(agentID) > 256 || len(sid) > 256 {
		return nil, errors.New("Invalid terminal identity")
	}
	digest := sha256.Sum256([]byte(agentID + "\x00" + sid))
	open := &sessiondv1.OpenTerminal{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_PTY}, Session: &sessiondv1.SessionRef{SessionId: fmt.Sprintf("pty-%x", digest[:24])}, Cwd: workspace, Cols: 80, Rows: 24}
	if strings.HasPrefix(connectionID, "wsl:") {
		argv, err := targetfs.BuildWSLCommand(targetfs.WSLConfig{Distro: strings.TrimPrefix(connectionID, "wsl:"), HelperPath: "/bin/sh"})
		if err != nil {
			return nil, err
		}
		open.Executable = &argv[0]
		open.Args = []string{"-d", strings.TrimPrefix(connectionID, "wsl:"), "--cd", workspace, "--exec", "/bin/sh", "-l"}
		open.Cwd = r.DefaultWorkspace
	} else if connectionID != "" && connectionID != "local" {
		connection, err := r.Store.GetSSHConnection(request.Context(), connectionID)
		if err != nil {
			return nil, err
		}
		if connection == nil {
			return nil, errors.New("SSH connection was not found")
		}
		settings, ok := connection["settings"].(map[string]any)
		if !ok {
			settings = connection
		}
		stringAt := func(key string) string { value, _ := settings[key].(string); return value }
		port := 22
		if value, ok := settings["port"].(int64); ok {
			port = int(value)
		}
		if value, ok := settings["port"].(int); ok {
			port = value
		}
		argv, err := targetfs.BuildSSHCommand(targetfs.SSHConfig{Host: stringAt("host"), Alias: stringAt("ssh_config_alias"), User: stringAt("user"), Port: port, IdentityFile: stringAt("identity_file"), ConfigFile: stringAt("config_file"), HelperPath: "/bin/sh"})
		if err != nil {
			return nil, err
		}
		argv[len(argv)-1] = "cd -- '" + strings.ReplaceAll(workspace, "'", "'\\''") + "' && exec \"${SHELL:-/bin/sh}\" -l"
		open.Executable = &argv[0]
		open.Args = append([]string{"-tt"}, argv[1:]...)
		open.Cwd = r.DefaultWorkspace
	}
	return open, nil
}
