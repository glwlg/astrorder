package botgroups

import (
	"context"
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
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type antigravityGuardServer struct {
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer
	createCalls int
	sendCalls   int
}

func (s *antigravityGuardServer) CreateSession(_ context.Context, _ *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	s.createCalls++
	return nil, nil
}

func (s *antigravityGuardServer) Send(_ context.Context, _ *pb.SendRequest) (*pb.CommandReceipt, error) {
	s.sendCalls++
	return nil, nil
}

func TestAntigravityBlockedFromAutomatedBotGroupsForCompliance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		machine string
		agentID string
		kind    string
	}{
		{name: "local", machine: "local", agentID: "local-antigravity", kind: "antigravity"},
		{name: "ssh", machine: "wsl-ssh", agentID: "ssh-antigravity-wsl-ssh", kind: "antigravity-ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			connID := ""
			if tc.machine != "local" {
				connID = tc.machine
				if _, err := st.SaveSSHConnection(ctx, map[string]any{
					"id":        tc.machine,
					"host":      "127.0.0.1",
					"port":      22,
					"user":      "remoteuser",
					"workspace": "/srv/group-ws",
				}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := st.UpsertAgent(ctx, map[string]any{
				"id":            tc.agentID,
				"kind":          tc.kind,
				"name":          "Antigravity",
				"connection_id": connID,
				"status":        "ready",
			}); err != nil {
				t.Fatal(err)
			}

			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			srv := grpc.NewServer()
			peer := &antigravityGuardServer{}
			pb.RegisterSessionControlServer(srv, peer)
			pb.RegisterRuntimeQueryServer(srv, peer)
			go srv.Serve(lis)
			defer srv.Stop()

			conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithInsecure())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			controller := &NativeController{
				Store:            st,
				Client:           &daemonclient.Client{Session: pb.NewSessionControlClient(conn), Runtime: pb.NewRuntimeQueryClient(conn)},
				Publisher:        &publisher{},
				PollInterval:     5 * time.Millisecond,
				DefaultWorkspace: "P:/WinDefault",
			}

			svc, err := New(ctx, Options{
				Store:      st,
				Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil),
				Publisher:  &publisher{},
				Controller: controller,
				Decider:    sendAllDecider,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()

			// 1. Creating a Bot Group with an Antigravity member must fail with 422.
			createBody := `{"name":"Test Group","members":[{"id":"m1","agent_id":"` + tc.agentID + `","machine_id":"` + tc.machine + `"}]}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/bot-groups", strings.NewReader(createBody))
			req.Header.Set("Authorization", "Bearer fixture")
			rec := httptest.NewRecorder()
			svc.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Google Antigravity") {
				t.Fatalf("expected 422 compliance rejection on group create, got %d: %s", rec.Code, rec.Body.String())
			}

			// 2. Even if a legacy group row existed in SQLite, NativeController.Run must fail closed before gRPC dispatch.
			member := Member{ID: "m1", AgentID: tc.agentID, MachineID: tc.machine}
			if _, err := controller.Run(ctx, Turn{
				GroupID:   "legacy-g1",
				Member:    member,
				CommandID: "cmd-1",
				Prompt:    "auto relay",
			}, func(string) error { return nil }); err == nil || !strings.Contains(err.Error(), "Google Antigravity") {
				t.Fatalf("expected NativeController compliance rejection, got %v", err)
			}
			if peer.createCalls != 0 || peer.sendCalls != 0 {
				t.Fatalf("expected zero gRPC dispatches to Antigravity, got create=%d send=%d", peer.createCalls, peer.sendCalls)
			}
		})
	}
}
