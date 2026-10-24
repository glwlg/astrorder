package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReconnectRecoveryBoundaries(t *testing.T) {
	for _, scenario := range []string{"manual disconnect", "failed native resume", "missing ownership", "wrong daemon", "wrong connection", "running", "waiting approval"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			reads, attaches, sends := 0, 0, 0
			srv := &mockDaemonServer{
				readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
					reads++
					if scenario == "failed native resume" {
						return nil, status.Error(codes.Unavailable, "failed")
					}
					return &pb.ModelState{}, nil
				},
				attachCb: func(*pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
					attaches++
					return nil, status.Error(codes.FailedPrecondition, "exit unconfirmed")
				},
				sendCb: func(*pb.SendRequest) (*pb.CommandReceipt, error) {
					sends++
					return nil, status.Error(codes.Internal, "never send")
				},
				statusCb: func() (*pb.GetStatusResponse, error) {
					state := pb.SessionStatus_SESSION_STATUS_IDLE
					if scenario == "running" {
						state = pb.SessionStatus_SESSION_STATUS_RUNNING
					}
					if scenario == "waiting approval" {
						state = pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL
					}
					snap := refreshOwnedSnapshot("s", "a", nil, pb.RuntimeKind_RUNTIME_KIND_CODEX, state)
					if scenario == "missing ownership" {
						owned := false
						snap.RuntimeOwned = &owned
					}
					if scenario == "wrong connection" {
						snap.Session.ConnectionId = refreshStringPtr("other")
					}
					daemonID := "test-daemon"
					if scenario == "wrong daemon" {
						daemonID = "foreign"
					}
					return &pb.GetStatusResponse{DaemonId: daemonID, Sessions: []*pb.SessionSnapshot{snap}}, nil
				},
			}
			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			seedRefreshAgent(t, st, "a", "codex", "")
			seedRefreshSession(t, st, "a", "s", map[string]any{"status": "error"})
			if scenario == "manual disconnect" {
				if err := st.SetAgentConnectionChoice(ctx, "local:codex", false); err != nil {
					t.Fatal(err)
				}
			}
			err := h.RecoverDisconnectedSessions(ctx, "local", "codex")
			row, _ := st.GetSession(ctx, "a", "s")
			want := "error"
			if scenario == "running" {
				want = "running"
			}
			if scenario == "waiting approval" {
				want = "waiting_approval"
			}
			if row["status"] != want || sends != 0 {
				t.Fatalf("state=%v want=%s sends=%d err=%v", row["status"], want, sends, err)
			}
			if want == "error" && err == nil {
				t.Fatal("unconfirmed recovery silently succeeded")
			}
			if scenario == "manual disconnect" && (reads != 0 || attaches != 0) {
				t.Fatalf("disabled Agent touched native runtime: %d %d", reads, attaches)
			}
		})
	}
}

func TestConnectHTTPRecoversSessionWithoutOpeningHistory(t *testing.T) {
	var attached atomic.Bool
	var sends, histories atomic.Int32
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			if !attached.Load() {
				return nil, status.Error(codes.Unavailable, "transport lost")
			}
			return &pb.ModelState{}, nil
		},
		attachCb: func(req *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			if !req.RuntimeControl {
				attached.Store(true)
			}
			return &pb.SessionReceipt{DaemonId: "test-daemon", Attached: true, Session: req.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		statusCb: func() (*pb.GetStatusResponse, error) {
			snaps := []*pb.SessionSnapshot{}
			if attached.Load() {
				snaps = append(snaps, refreshOwnedSnapshot("s", "a", nil, pb.RuntimeKind_RUNTIME_KIND_CODEX, pb.SessionStatus_SESSION_STATUS_IDLE))
			}
			return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: snaps}, nil
		},
		sendCb: func(*pb.SendRequest) (*pb.CommandReceipt, error) {
			sends.Add(1)
			return nil, status.Error(codes.Internal, "never send")
		},
		getHistoryPageCb: func(*pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
			histories.Add(1)
			return nil, status.Error(codes.Internal, "never load history")
		},
	}
	h, st, client, cleanup := setupTestServer(t, srv)
	defer cleanup()
	seedRefreshAgent(t, st, "a", "codex", "")
	seedRefreshSession(t, st, "a", "s", map[string]any{"status": "error"})
	conn, err := connections.NewHandler(connections.Options{Store: st, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	conn.SetSessionRecovery(ctx, func(ctx context.Context, c, k string) error {
		err := h.RecoverDisconnectedSessions(ctx, c, k)
		finished <- err
		return err
	})
	w := httptest.NewRecorder()
	conn.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/agents/codex/connect", nil))
	if w.Code != 200 {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery timed out")
	}
	row, _ := st.GetSession(ctx, "a", "s")
	if row["status"] != "idle" || sends.Load() != 0 || histories.Load() != 0 {
		t.Fatalf("status=%v sends=%d histories=%d", row["status"], sends.Load(), histories.Load())
	}
}
