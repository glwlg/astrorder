package botgroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type artifactHistory struct {
	replies     map[string]string
	operations  map[string][]NativeFileOperation
	evidenceErr error
}

func (*artifactHistory) Run(context.Context, Turn, func(string) error) (Reply, error) {
	panic("history must not send")
}
func (*artifactHistory) Interrupt(context.Context, Turn) error { panic("history must not interrupt") }
func (h *artifactHistory) ReadReply(_ context.Context, _, _ string, ids []string) (string, error) {
	return h.replies[ids[0]], nil
}
func (h *artifactHistory) ReadArtifactEvidence(_ context.Context, _, _ string, ids []string) ([]NativeFileOperation, error) {
	return h.operations[ids[0]], h.evidenceErr
}

type artifactClassifyFunc func(context.Context, ArtifactInput) (map[string]string, error)

func (f artifactClassifyFunc) ClassifyArtifacts(ctx context.Context, input ArtifactInput) (map[string]string, error) {
	return f(ctx, input)
}

type groupArtifactFS struct {
	targetfs.FileSystem
	resolver   *groupArtifactResolver
	connection string
}

func (f groupArtifactFS) Stat(ctx context.Context, path string) (targetfs.Info, error) {
	if f.resolver.onStat != nil {
		f.resolver.onStat(ctx)
	}
	info, ok := f.resolver.files[f.connection+"::"+path]
	if !ok {
		return targetfs.Info{}, &targetfs.Error{Status: 404, Detail: "not found"}
	}
	return info, nil
}

type groupArtifactResolver struct {
	files  map[string]targetfs.Info
	mu     sync.Mutex
	calls  []targetfs.ResolveRequest
	fail   error
	onStat func(context.Context)
}

func (r *groupArtifactResolver) Resolve(_ context.Context, request targetfs.ResolveRequest) (targetfs.Resolved, error) {
	r.mu.Lock()
	r.calls = append(r.calls, request)
	r.mu.Unlock()
	return targetfs.Resolved{FileSystem: groupArtifactFS{resolver: r, connection: request.ConnectionID}, ConnectionID: request.ConnectionID}, r.fail
}

func artifactFixture(t *testing.T) (*Service, *artifactHistory, *groupArtifactResolver, *publisher) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, source := range []struct{ agent, session, workspace, connection string }{
		{"dev", "s-dev", "/home/fixture/project", "ssh-dev"},
		{"design", "s-design", "C:/project", "local"},
		{"other", "s-other", "/home/fixture/project", "ssh-other"},
	} {
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": source.agent, "name": source.agent, "kind": "codex", "status": "ready", "connection_id": source.connection}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSession(ctx, map[string]any{"id": source.session, "agent_id": source.agent, "workspace": source.workspace, "connection_id": source.connection, "status": "idle", "updated_at": now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetPreference(ctx, "bot_group:g", Group{ID: "g", Members: []Member{}, MemberStates: map[string]MemberState{}}, true); err != nil {
		t.Fatal(err)
	}
	h := &artifactHistory{replies: map[string]string{}, operations: map[string][]NativeFileOperation{}}
	r := &groupArtifactResolver{files: map[string]targetfs.Info{}}
	p := &publisher{}
	classifier := artifactClassifyFunc(func(_ context.Context, input ArtifactInput) (map[string]string, error) {
		out := map[string]string{}
		for _, candidate := range input.Candidates {
			out[candidate.File.ID] = candidate.Operations[0].ID
		}
		return out, nil
	})
	return &Service{options: Options{Store: st, Publisher: p, Controller: h, FileResolver: r, ArtifactDecider: classifier}}, h, r, p
}
func artifactMessage(id, agent string) Message {
	return Message{ID: id, GroupID: "g", SenderType: "agent", SenderID: agent, SenderMemberID: agent, SenderName: agent, SessionID: "s-" + agent, NativeIDs: []string{id}, CreatedAt: now()}
}
func appendArtifactMessage(t *testing.T, s *Service, message Message) {
	t.Helper()
	if err := s.appendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}
func runArtifactIndex(t *testing.T, s *Service) groupFiles {
	t.Helper()
	if err := s.indexGroupArtifacts(context.Background(), "g"); err != nil {
		t.Fatal(err)
	}
	files, err := s.artifactDirectory(context.Background(), "g")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestVerifiedReferencesAndDeliveredArtifactsAreSeparate(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	h.replies["m1"] = "[方案](docs/方案.md) [同一方案](/home/fixture/project/docs/方案.md:2)\n" +
		"![架构图](output/chart.png)\n[skill](/home/fixture/.codex/skills/astrorder-blackboard/SKILL.md)\n" +
		"`operation_log.py` `tmp/test-result.json` `api-review.example-project.assessment` `method-contract.list` `$.data.records[*]` `$.contractId` " +
		"`does-not-exist.go` [目录](docs/folder.md) [管道](pipe.log)"
	for _, p := range []string{"/home/fixture/project/docs/方案.md", "/home/fixture/project/output/chart.png", "/home/fixture/.codex/skills/astrorder-blackboard/SKILL.md", "/home/fixture/project/operation_log.py", "/home/fixture/project/tmp/test-result.json"} {
		r.files["ssh-dev::"+p] = targetfs.Info{Mode: 0600}
	}
	r.files["ssh-dev::/home/fixture/project/docs/folder.md"] = targetfs.Info{IsDir: true, Mode: fs.ModeDir}
	r.files["ssh-dev::/home/fixture/project/pipe.log"] = targetfs.Info{Mode: fs.ModeNamedPipe}
	h.operations["m1"] = []NativeFileOperation{
		{ID: "patch", Kind: "fileChange", Path: "docs/方案.md"},
		{ID: "image", Kind: "commandExecution", Cwd: "/home/fixture/project", Command: "python -c 'plt.savefig(\"output/chart.png\")'"},
		{ID: "test", Kind: "fileChange", Path: "tmp/test-result.json"},
	}
	s.options.ArtifactDecider = artifactClassifyFunc(func(_ context.Context, input ArtifactInput) (map[string]string, error) {
		if input.Request != "交付技术方案与架构图" {
			t.Fatalf("lost the user's delivery request: %q", input.Request)
		}
		if len(input.Candidates) != 3 {
			t.Fatalf("nonchanges offered as deliverables: %+v", input.Candidates)
		}
		out := map[string]string{}
		for _, c := range input.Candidates {
			if c.File.Name != "test-result.json" {
				out[c.File.ID] = c.Operations[0].ID
			}
		}
		return out, nil
	})
	message := artifactMessage("m1", "dev")
	message.Text = h.replies["m1"]
	appendArtifactMessage(t, s, Message{ID: "u1", GroupID: "g", SenderType: "user", Text: "交付技术方案与架构图"})
	appendArtifactMessage(t, s, message)
	before, _ := s.options.Store.GetPreference(context.Background(), artifacts.DatabaseKey("g"))
	if before.Exists {
		t.Fatal("unverified text published an artifact during message append")
	}
	files := runArtifactIndex(t, s)
	if len(files.Items) != 2 || len(files.References) != 5 || files.Error != "" {
		t.Fatalf("files=%+v", files)
	}
	for _, file := range files.Items {
		if file.EvidenceID == "" || file.ContentKey == "" || file.ConnectionID != "ssh-dev" {
			t.Fatalf("unproven artifact: %+v", file)
		}
	}
	for _, file := range files.References {
		if file.Name == "SKILL.md" && file.ContentKey != "" {
			t.Fatal("existing skill became an artifact")
		}
	}
	pref, _ := s.options.Store.GetPreference(context.Background(), "bot_group_msgs:g")
	raw, _ := json.Marshal(pref.Value)
	if strings.Contains(string(raw), "方案.md") {
		t.Fatal("reply text copied from native history")
	}
}

func TestRebuildRemovesLegacyGuessesAndPreservesFirstSource(t *testing.T) {
	s, h, r, p := artifactFixture(t)
	ctx := context.Background()
	if err := s.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		return artifacts.WriteTx(ctx, tx, "g", artifacts.Directory{Items: []artifacts.File{{Path: "$.contractId"}, {Path: "SKILL.md"}}})
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.options.Store.SetPreference(ctx, "bot_group_artifacts_cursor:g", "m3", true)
	h.replies["m1"] = "[原件](docs/方案.md)"
	h.replies["m2"] = "[Windows转发](/home/fixture/project/docs/方案.md)"
	h.replies["m3"] = "[同名Linux转发](docs/方案.md)"
	r.files["ssh-dev::/home/fixture/project/docs/方案.md"] = targetfs.Info{}
	h.operations["m1"] = []NativeFileOperation{{ID: "patch", Kind: "fileChange", Path: "docs/方案.md"}}
	for _, pair := range []struct{ id, agent string }{{"m1", "dev"}, {"m2", "design"}, {"m3", "other"}} {
		appendArtifactMessage(t, s, artifactMessage(pair.id, pair.agent))
	}
	files := runArtifactIndex(t, s)
	if len(files.Items) != 1 || files.Items[0].AgentID != "dev" || files.Items[0].ConnectionID != "ssh-dev" {
		t.Fatalf("first source changed: %+v", files)
	}
	if len(r.calls) != 1 || r.calls[0].SessionID != "" || r.calls[0].ConnectionID != "ssh-dev" {
		t.Fatalf("forwarding probed another machine: %v", r.calls)
	}
	events := len(p.events)
	_ = runArtifactIndex(t, s)
	if len(p.events) != events {
		t.Fatal("polling duplicated artifact changes")
	}
	if pref, _ := s.options.Store.GetPreference(ctx, "bot_group_artifacts_cursor:g"); pref.Exists {
		t.Fatal("old cursor not removed")
	}
}

func TestExistenceAndRecentTimestampWithoutNativeChangesDoNotMakeArtifact(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	h.replies["m1"] = "已生成 [方案](docs/方案.md)"
	r.files["ssh-dev::/home/fixture/project/docs/方案.md"] = targetfs.Info{ModTime: time.Now()}
	s.options.ArtifactDecider = artifactClassifyFunc(func(context.Context, ArtifactInput) (map[string]string, error) {
		t.Fatal("no native changes should reach classifier")
		return nil, nil
	})
	appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
	files := runArtifactIndex(t, s)
	if len(files.Items) != 0 || len(files.References) != 1 {
		t.Fatalf("existence became proof: %+v", files)
	}
}

func TestNativeFailureAndInventedEvidenceDoNotPublish(t *testing.T) {
	for _, failure := range []string{"native", "model"} {
		t.Run(failure, func(t *testing.T) {
			s, h, r, _ := artifactFixture(t)
			h.replies["m1"] = "[方案](docs/方案.md)"
			r.files["ssh-dev::/home/fixture/project/docs/方案.md"] = targetfs.Info{}
			h.operations["m1"] = []NativeFileOperation{{ID: "actual", Kind: "fileChange", Path: "docs/方案.md"}}
			if failure == "native" {
				h.evidenceErr = errors.New("native offline")
			} else {
				s.options.ArtifactDecider = artifactClassifyFunc(func(_ context.Context, input ArtifactInput) (map[string]string, error) {
					return map[string]string{input.Candidates[0].File.ID: "invented"}, nil
				})
			}
			appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
			files := runArtifactIndex(t, s)
			if len(files.Items) != 0 || len(files.References) != 1 || files.Error == "" {
				t.Fatalf("uncertain evidence accepted: %+v", files)
			}
		})
	}
}

func TestSourceOutageDoesNotRebindToForwardingMachine(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	h.replies["m1"] = "[方案](docs/方案.md)"
	h.replies["m2"] = "[转发](/home/fixture/project/docs/方案.md)"
	r.fail = errors.New("ssh-dev offline")
	appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
	appendArtifactMessage(t, s, artifactMessage("m2", "design"))
	files := runArtifactIndex(t, s)
	if len(files.Items) != 0 || len(files.References) != 0 || files.Error == "" || len(r.calls) != 1 {
		t.Fatalf("outage lost source: files=%+v calls=%v", files, r.calls)
	}
}

func TestMissingRemoteSessionConnectionNeverBecomesLocalFileProbe(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	h.replies["m1"] = "[方案](docs/方案.md)"
	if err := s.options.Store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET connection_id=NULL WHERE agent_id='dev' AND id='s-dev'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
	files := runArtifactIndex(t, s)
	if len(files.Items) != 0 || len(files.References) != 0 || len(r.calls) != 0 || !strings.Contains(files.Error, "来源连接不一致") {
		t.Fatalf("missing connection guessed a source: %+v %v", files, r.calls)
	}
}

func TestExplicitRecheckRetriesFailedEvidenceAndPreservesOtherBlackboardKeys(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	ctx := context.Background()
	h.replies["m1"] = "[方案](docs/方案.md)"
	h.operations["m1"] = []NativeFileOperation{{ID: "patch", Kind: "fileChange", Path: "docs/方案.md"}}
	r.files["ssh-dev::/home/fixture/project/docs/方案.md"] = targetfs.Info{}
	h.evidenceErr = errors.New("native offline")
	appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
	first := runArtifactIndex(t, s)
	if first.Error == "" || len(first.Items) != 0 {
		t.Fatalf("failed evidence was accepted: %+v", first)
	}
	if err := s.options.Store.SetPreference(ctx, "blackboard:group:g:contract", "original contract", true); err != nil {
		t.Fatal(err)
	}
	h.evidenceErr = nil
	if _, err := s.recheckArtifacts(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	files := runArtifactIndex(t, s)
	if files.Error != "" || len(files.Items) != 1 || files.Items[0].EvidenceID != "patch" {
		t.Fatalf("explicit recheck failed: %+v", files)
	}
	contract, err := s.options.Store.GetPreference(ctx, "blackboard:group:g:contract")
	if err != nil || contract.Value != "original contract" {
		t.Fatal("file recheck changed unrelated blackboard contents")
	}
}

func TestBackgroundValidationDoesNotBlockMessagesAndCannotResurrectDeletedGroup(t *testing.T) {
	s, h, r, _ := artifactFixture(t)
	h.replies["m1"] = "[方案](docs/方案.md)"
	r.files["ssh-dev::/home/fixture/project/docs/方案.md"] = targetfs.Info{}
	h.operations["m1"] = []NativeFileOperation{{ID: "patch", Kind: "fileChange", Path: "docs/方案.md"}}
	started, release := make(chan struct{}), make(chan struct{})
	r.onStat = func(context.Context) { close(started); <-release }
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.active = map[string]map[string]*activeTurn{}
	appendArtifactMessage(t, s, artifactMessage("m1", "dev"))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	readDone := make(chan error, 1)
	go func() { _, err := s.messages(context.Background(), "g"); readDone <- err }()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("file validation blocked group history")
	}
	if err := s.remove(context.Background(), "g"); err != nil {
		t.Fatal(err)
	}
	close(release)
	s.Close()
	for _, key := range []string{fileIndexKey("g"), artifacts.DatabaseKey("g")} {
		if pref, _ := s.options.Store.GetPreference(context.Background(), key); pref.Exists {
			t.Fatalf("worker resurrected %s", key)
		}
	}
}
