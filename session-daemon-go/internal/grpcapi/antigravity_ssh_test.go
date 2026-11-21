package grpcapi_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/antigravity"
	"astrorder.dev/session-daemon/internal/runtime/remote"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

type syntheticAgySSHSession struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}

	mu         sync.Mutex
	closed     bool
	exitCode   int
	remoteExit bool
}

func newSyntheticAgySSHSession() *syntheticAgySSHSession {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	return &syntheticAgySSHSession{
		stdinR:  inR,
		stdinW:  inW,
		stdoutR: outR,
		stdoutW: outW,
		done:    make(chan struct{}),
	}
}

func (s *syntheticAgySSHSession) Stdin() io.WriteCloser { return s.stdinW }
func (s *syntheticAgySSHSession) Stdout() io.ReadCloser { return s.stdoutR }
func (s *syntheticAgySSHSession) Stderr() []byte        { return nil }

func (s *syntheticAgySSHSession) finish(code int) {
	s.mu.Lock()
	if !s.closed {
		s.exitCode = code
		s.remoteExit = true
	}
	s.mu.Unlock()
	_ = s.stdoutW.Close()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

func (s *syntheticAgySSHSession) Wait() (ssh.ExitStatus, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ssh.ExitStatus{LocalKilled: true, ExitCode: -1, Err: ssh.ErrSessionClosed}, ssh.ErrSessionClosed
	}
	return ssh.ExitStatus{ConfirmedRemoteExit: s.remoteExit, ExitCode: s.exitCode}, nil
}

func (s *syntheticAgySSHSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.stdinW.Close()
	_ = s.stdinR.Close()
	_ = s.stdoutW.Close()
	_ = s.stdoutR.Close()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func TestAntigravitySSHOfficialGRPCLifecycle(t *testing.T) {
	f := startFixture(t)

	connID := "wsl"
	aid := "ssh-antigravity-" + connID
	parentID := "11111111-1111-4111-8111-111111111111"
	deletableID := "22222222-2222-4222-8222-222222222222"

	var stateMu sync.Mutex
	deletedTarget := false
	recordedSpecs := []ssh.CommandSpec{}

	realFactory := remote.NewRemoteAntigravityFactory(f.daemon.Emit, "")
	multiplex := core.NewMultiplexRegistry(func(cid string, settings map[string]any) (core.Adapter, error) {
		realAdapter, err := realFactory(cid, settings)
		if err != nil || realAdapter == nil {
			return nil, fmt.Errorf("production remote antigravity factory rejected settings: %w", err)
		}

		exe, _ := settings["antigravity_executable"].(string)
		ws, _ := settings["workspace"].(string)
		disp, _ := settings["display_name"].(string)

		starter := func(_ context.Context, spec ssh.CommandSpec) (antigravity.SSHSession, error) {
			if _, err := ssh.BuildRemoteCommand(spec); err != nil {
				return nil, err
			}
			stateMu.Lock()
			recordedSpecs = append(recordedSpecs, spec)
			stateMu.Unlock()

			sess := newSyntheticAgySSHSession()
			for _, arg := range spec.Args {
				if arg == "models" {
					go func() {
						_, _ = fmt.Fprintln(sess.stdoutW, "gemini-test\tTest Gemini")
						_, _ = fmt.Fprintln(sess.stdoutW, "claude-test\tTest Claude")
						sess.finish(0)
					}()
					return sess, nil
				}
			}

			go func() {
				serveMockGRPCACP(sess.stdinR, sess.stdoutW, parentID)
				sess.finish(0)
			}()
			return sess, nil
		}

		helper := func(_ context.Context, payload map[string]any) (map[string]any, error) {
			action, _ := payload["action"].(string)
			switch action {
			case "session.list":
				items := []map[string]any{{
					"id":                parentID,
					"title":             "Remote Session",
					"workspace":         ws,
					"status":            "idle",
					"updated_at":        "2026-11-17T16:00:00Z",
					"history_state":     "available",
					"control_state":     "unbound",
					"native_kind":       "rpc",
					"source_session_id": parentID,
				}}
				return map[string]any{"items": items}, nil
			case "inspect_conversation":
				return map[string]any{"desktop_readonly": false, "resumable": true}, nil
			case "session_usage":
				return map[string]any{"context_available": false, "provider": "antigravity"}, nil
			case "history_page":
				return map[string]any{
					"items": []map[string]any{
						{"id": "antigravity:" + parentID + ":0", "role": "user", "kind": "message", "text": "fixture text"},
						{"id": "antigravity:" + parentID + ":1", "role": "assistant", "kind": "message", "text": "remote fixture response"},
					},
					"next_cursor": nil,
					"has_more":    false,
				}, nil
			case "delete_unbound":
				stateMu.Lock()
				deletedTarget = true
				stateMu.Unlock()
				return map[string]any{"confirmed": true}, nil
			}
			return nil, fmt.Errorf("unexpected helper action %q", action)
		}

		return antigravity.NewWithSSH(antigravity.Config{
			Executable:   exe,
			Workspace:    ws,
			Allowed:      []string{"/"},
			AgentID:      "ssh-antigravity-" + cid,
			AgentName:    "Antigravity · " + disp,
			ConnectionID: cid,
			Emit:         f.daemon.Emit,
		}, starter, helper), nil
	})
	defer multiplex.Close()
	f.daemon.RegisterRuntime("antigravity-ssh", multiplex)

	control := pb.NewSessionControlClient(f.conn)
	query := pb.NewRuntimeQueryClient(f.conn)
	daemonCtl := pb.NewDaemonControlClient(f.conn)

	sshCfg := &pb.SshConnectConfig{
		Host:                  "127.0.0.1",
		Port:                  22,
		User:                  ptr("fixture"),
		Workspace:             ptr("/home/fixture/workspace/example-org/astrorder"),
		DisplayName:           ptr("WSL"),
		AntigravityExecutable: ptr("/home/fixture/.local/bin/agy"),
	}
	target := &pb.RuntimeTarget{
		Runtime:      pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH,
		AgentId:      &aid,
		ConnectionId: &connID,
		Ssh:          sshCfg,
	}
	probe := &pb.SessionRef{
		SessionId:    "probe-antigravity-" + connID,
		ConnectionId: &connID,
	}

	// 1. Attach runtime control session & verify connector.hello + GetStatus live ownership
	attachResp, err := control.AttachSession(f.ctx, &pb.AttachSessionRequest{
		Target:         target,
		Session:        probe,
		RuntimeControl: true,
	})
	if err != nil || !attachResp.GetAttached() || attachResp.GetSession().GetConnectionId() != connID {
		t.Fatalf("AttachSession(RuntimeControl): %+v %v", attachResp, err)
	}

	probeReplay, err := f.daemon.ReplayPage(probe.SessionId, 0, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundHello := false
	for _, fr := range probeReplay.Frames {
		if fr.Event == "connector.hello" {
			if fr.Payload["kind"] == "antigravity" && fr.Payload["id"] == aid && fr.Payload["connection_id"] == connID && fr.Payload["name"] == "Antigravity · WSL" {
				foundHello = true
			}
		}
	}
	if !foundHello {
		t.Fatalf("expected connector.hello on remote probe stream, got %+v", probeReplay.Frames)
	}

	stResp, err := daemonCtl.GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	foundRuntimeStatus := false
	for _, rt := range stResp.Runtimes {
		if rt.GetRuntime() == pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH && rt.GetRegistered() && rt.GetOwnedSessions() == 1 {
			foundRuntimeStatus = true
		}
	}
	foundProbeOwned := false
	for _, sess := range stResp.Sessions {
		if sess.GetSession().GetSessionId() == probe.SessionId && sess.GetRuntimeOwned() && sess.GetRuntime() == pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH && sess.GetSession().GetConnectionId() == connID && sess.GetSession().GetAgentId() == aid {
			foundProbeOwned = true
		}
	}
	if !foundRuntimeStatus || !foundProbeOwned {
		t.Fatalf("GetStatus missing live ANTIGRAVITY_SSH ownership: runtime=%v probe=%v", foundRuntimeStatus, foundProbeOwned)
	}

	// 2. Native queries (models.list & session.list)
	if _, err := query.QueryNative(f.ctx, &pb.NativeQueryRequest{
		Target: target,
		Query:  &pb.NativeQueryRequest_AntigravityModelsList{AntigravityModelsList: &pb.AntigravityModelsListQuery{}},
	}); err != nil {
		t.Fatalf("QueryNative AntigravityModelsList: %v", err)
	}
	if _, err := query.QueryNative(f.ctx, &pb.NativeQueryRequest{
		Target: target,
		Query:  &pb.NativeQueryRequest_AntigravitySessionList{AntigravitySessionList: &pb.AntigravitySessionListQuery{}},
	}); err != nil {
		t.Fatalf("QueryNative AntigravitySessionList: %v", err)
	}

	// 3. CreateSession, SetModel, SetApprovalMode, SetReasoning, Send, GetHistoryPage
	created, err := control.CreateSession(f.ctx, &pb.CreateSessionRequest{
		Meta:   controlMeta("agy-ssh-create"),
		Target: target,
		Options: &pb.CreateSessionRequest_Antigravity{
			Antigravity: &pb.AntigravityCreateOptions{Cwd: "/home/fixture/workspace/example-org/astrorder"},
		},
	})
	if err != nil || created.GetCompleted() == nil || created.GetSession().GetConnectionId() != connID || created.GetSession().GetAgentId() != aid {
		t.Fatalf("CreateSession: %+v %v", created, err)
	}
	ref := created.Session

	if switched, err := query.SetModel(f.ctx, &pb.SetModelRequest{Meta: controlMeta("agy-ssh-model"), Session: ref, Provider: "antigravity", Model: "claude-test"}); err != nil || !switched.GetAccepted() {
		t.Fatalf("SetModel: %+v %v", switched, err)
	}
	if appSet, err := query.SetApprovalMode(f.ctx, &pb.SetApprovalModeRequest{Meta: controlMeta("agy-ssh-approval"), Session: ref, Mode: "auto"}); err != nil || !appSet.GetAccepted() {
		t.Fatalf("SetApprovalMode: %+v %v", appSet, err)
	}

	// External HTTP image URL must be rejected for antigravity-ssh!
	rejectReceipt, err := control.Send(f.ctx, &pb.SendRequest{
		Meta:    controlMeta("agy-ssh-reject-img"),
		Session: ref,
		Input: []*pb.InputPart{
			{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}},
			{Part: &pb.InputPart_ImageUrl{ImageUrl: &pb.ImageUrlInput{Url: "https://example.invalid/x.png"}}},
		},
	})
	if err == nil && rejectReceipt.GetCompleted() != nil {
		t.Fatalf("expected external image URL rejection on antigravity-ssh, got %+v", rejectReceipt)
	}

	sendReceipt, err := control.Send(f.ctx, &pb.SendRequest{
		Meta:    controlMeta("agy-ssh-send"),
		Session: ref,
		Input:   []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}}},
	})
	if err != nil || sendReceipt.GetCompleted() == nil || !sendReceipt.GetAccepted() {
		t.Fatalf("Send: %+v %v", sendReceipt, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for multiplex.Snapshot()[ref.SessionId].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if multiplex.Snapshot()[ref.SessionId].Status != "idle" {
		t.Fatalf("remote turn did not settle to idle: %+v", multiplex.Snapshot()[ref.SessionId])
	}

	histPage, err := query.GetHistoryPage(f.ctx, &pb.HistoryPageRequest{Session: ref, Limit: 20})
	if err != nil || len(histPage.GetItems()) != 2 {
		t.Fatalf("GetHistoryPage: %+v %v", histPage, err)
	}

	// 4. Fork rejected before dispatch; DeleteSession removes via helper!
	forkAttempt, err := control.CreateSession(f.ctx, &pb.CreateSessionRequest{
		Meta:   controlMeta("agy-ssh-fork"),
		Target: target,
		Options: &pb.CreateSessionRequest_Antigravity{
			Antigravity: &pb.AntigravityCreateOptions{
				Cwd:             "/home/fixture/workspace/example-org/astrorder",
				ParentSessionId: &ref.SessionId,
			},
		},
	})
	if err == nil && forkAttempt.GetRejected() == nil {
		t.Fatalf("expected ACP fork rejection, got %+v", forkAttempt)
	}

	delRef := &pb.SessionRef{SessionId: deletableID, AgentId: &aid, ConnectionId: &connID}
	if _, err := control.AttachSession(f.ctx, &pb.AttachSessionRequest{
		Target:        target,
		Session:       delRef,
		NativeOptions: &pb.NativeJson{Schema: "antigravity.attach.v1", Utf8Json: []byte(`{"delete_only":true}`)},
	}); err != nil {
		t.Fatalf("AttachSession delete_only: %v", err)
	}
	delReceipt, err := control.DeleteSession(f.ctx, &pb.DeleteSessionRequest{
		Meta:    controlMeta("agy-ssh-delete"),
		Session: delRef,
	})
	if err != nil || delReceipt.GetCompleted() == nil {
		t.Fatalf("DeleteSession: %+v %v", delReceipt, err)
	}
	stateMu.Lock()
	wasDeleted := deletedTarget
	stateMu.Unlock()
	if !wasDeleted {
		t.Fatal("expected helper delete_unbound to be invoked")
	}

	// 5. CloseSession & DisconnectRuntime
	if _, err := control.CloseSession(f.ctx, &pb.CloseSessionRequest{Meta: controlMeta("agy-ssh-close"), Session: ref}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	discResp, err := query.DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta:   controlMeta("agy-ssh-disconnect"),
		Target: target,
	})
	if err != nil || !discResp.GetDisconnected() {
		t.Fatalf("DisconnectRuntime: %+v %v", discResp, err)
	}

	stateMu.Lock()
	specCount := len(recordedSpecs)
	firstExe := ""
	if specCount > 0 {
		firstExe = recordedSpecs[0].Executable
	}
	stateMu.Unlock()
	if specCount == 0 || firstExe != "/home/fixture/.local/bin/agy" {
		t.Fatalf("expected recorded SSH specs for /home/fixture/.local/bin/agy, got count=%d first=%q", specCount, firstExe)
	}
}
