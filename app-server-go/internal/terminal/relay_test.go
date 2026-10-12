package terminal

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"github.com/coder/websocket"
	"google.golang.org/grpc"
)

type terminalFixture struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedTerminalStreamServer
	frames   chan *sessiondv1.TerminalClientFrame
	canceled chan struct{}
}

func (f *terminalFixture) GetCapabilities(context.Context, *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	services := []string{}
	for _, name := range []string{"ConnectorIngress", "DaemonControl", "EventStream", "ModelConfig", "OperationQuery", "RuntimeQuery", "SessionControl", "TerminalStream"} {
		services = append(services, "astrorder.sessiond.v1."+name)
	}
	return &sessiondv1.GetCapabilitiesResponse{ProtocolMajor: 1, DaemonId: "fixture", Services: services,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1, OperationLookup: true,
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1}}, nil
}
func (f *terminalFixture) Open(stream grpc.BidiStreamingServer[sessiondv1.TerminalClientFrame, sessiondv1.TerminalServerFrame]) error {
	defer close(f.canceled)
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	f.frames <- first
	if err = stream.Send(&sessiondv1.TerminalServerFrame{TerminalStreamId: first.TerminalStreamId, Frame: &sessiondv1.TerminalServerFrame_Opened{Opened: &sessiondv1.TerminalOpened{Session: first.GetOpen().Session}}}); err != nil {
		return err
	}
	for _, data := range [][]byte{{0xe4}, {0xb8, 0xad}, {0xff, 'A'}} {
		if err = stream.Send(&sessiondv1.TerminalServerFrame{TerminalStreamId: first.TerminalStreamId, Frame: &sessiondv1.TerminalServerFrame_Output{Output: &sessiondv1.TerminalOutput{Data: data}}}); err != nil {
			return err
		}
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		f.frames <- frame
		if frame.GetClose() != nil {
			return stream.Send(&sessiondv1.TerminalServerFrame{TerminalStreamId: first.TerminalStreamId, Frame: &sessiondv1.TerminalServerFrame_Exit{Exit: &sessiondv1.TerminalExit{Cause: sessiondv1.TerminalExitCause_TERMINAL_EXIT_CAUSE_EXPLICIT_CLOSE}}})
		}
	}
}
func TestRelayUsesGRPCAndPreservesSplitUTF8(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &terminalFixture{frames: make(chan *sessiondv1.TerminalClientFrame, 8), canceled: make(chan struct{})}
	server := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(server, fixture)
	sessiondv1.RegisterTerminalStreamServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: listener.Addr().String(), Credential: "fixture", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	relay := &Relay{Client: client, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), DefaultWorkspace: t.TempDir()}
	httpServer := httptest.NewServer(relay)
	defer httpServer.Close()
	browser, _, err := websocket.Dial(ctx, strings.Replace(httpServer.URL, "http:", "ws:", 1)+"?token=fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	first := <-fixture.frames
	if first.GetOpen() == nil || first.GetOpen().Target.Runtime != sessiondv1.RuntimeKind_RUNTIME_KIND_PTY {
		t.Fatal("missing typed PTY open")
	}
	var output string
	for output != "中�A" {
		_, chunk, err := browser.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		output += string(chunk)
	}
	if err = browser.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":40}`)); err != nil {
		t.Fatal(err)
	}
	frame := <-fixture.frames
	if frame.GetResize().Cols != 100 || frame.GetResize().Rows != 40 {
		t.Fatal("resize lost")
	}
	if err = browser.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if string((<-fixture.frames).GetInput().Data) != "hello" {
		t.Fatal("input lost")
	}
	_ = browser.CloseNow()
	select {
	case <-fixture.canceled:
	case <-ctx.Done():
		t.Fatal("browser disconnect did not cancel attachment")
	}
	select {
	case frame := <-fixture.frames:
		if frame.GetClose() != nil {
			t.Fatal("disconnect terminated daemon PTY")
		}
	default:
	}
}
func TestResolveRequiresExactAgentForAmbiguousSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, agent := range []string{"first", "second"} {
		_, err = st.UpsertSession(ctx, map[string]any{"id": "shared", "agent_id": agent, "title": "", "status": "idle", "workspace": t.TempDir(), "updated_at": time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	relay := &Relay{Store: st}
	request := httptest.NewRequest(http.MethodGet, "/?session_id=shared", nil)
	if _, err = relay.resolve(request); err == nil {
		t.Fatal("ambiguous session accepted")
	}
	request = httptest.NewRequest(http.MethodGet, "/?session_id=shared&agent_id=first", nil)
	open, err := relay.resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	if open.Session.AgentId != nil || !strings.HasPrefix(open.Session.SessionId, "pty-") {
		t.Fatal("new PTY binding must be separate exact runtime identity")
	}
	body, _ := json.Marshal(open)
	if strings.Contains(string(body), "credential") {
		t.Fatal("credential leaked")
	}
	_, _ = io.Discard.Write(body)
}
