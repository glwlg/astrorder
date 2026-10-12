package grpcapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type echoNativeQuery struct{}

func (echoNativeQuery) Create(context.Context, core.Request) (core.Result, error) {
	panic("query must not create")
}
func (echoNativeQuery) Query(_ context.Context, r core.Request) (map[string]any, error) {
	return map[string]any{"method": r.Fields["method"], "params": r.Fields["request_params"]}, nil
}

func TestQueryNativeRoutesClosedSelectorsAndRemoteConnections(t *testing.T) {
	f := startFixture(t)
	for _, kind := range []string{"codex", "hermes", "grok"} {
		f.daemon.RegisterRuntime(kind, echoNativeQuery{})
	}
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	cases := []struct {
		branch, method string
		kind           sessiondv1.RuntimeKind
	}{
		{"codex_initialize", "initialize", 1}, {"codex_account_read", "account/read", 1}, {"codex_hooks_list", "hooks/list", 1}, {"codex_model_list", "model/list", 1}, {"codex_skills_list", "skills/list", 1}, {"codex_thread_items_list", "thread/items/list", 1}, {"codex_thread_list", "thread/list", 1}, {"codex_thread_loaded_list", "thread/loaded/list", 1}, {"codex_thread_read", "thread/read", 1},
		{"hermes_session_list", "session.list", 3}, {"hermes_active_list", "session.active_list", 3}, {"hermes_projects_tree", "projects.tree", 3}, {"hermes_project_sessions", "projects.project_sessions", 3}, {"hermes_session_history", "session.history", 3}, {"hermes_models_list", "models.list", 3}, {"hermes_commands_list", "commands.list", 3}, {"grok_initialize", "initialize", 6},
	}
	for _, test := range cases {
		t.Run(test.branch, func(t *testing.T) {
			request := &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: test.kind}}
			message := request.ProtoReflect()
			branch := message.Descriptor().Fields().ByName(protoreflect.Name(test.branch))
			query := message.NewField(branch).Message()
			for _, name := range []protoreflect.Name{"thread_id", "session_id", "project_id"} {
				if field := query.Descriptor().Fields().ByName(name); field != nil {
					query.Set(field, protoreflect.ValueOfString("native-id"))
				}
			}
			if test.branch == "codex_thread_items_list" {
				query.Set(query.Descriptor().Fields().ByName("limit"), protoreflect.ValueOfUint32(10))
			}
			message.Set(branch, protoreflect.ValueOfMessage(query))
			response, err := client.QueryNative(f.ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err = json.Unmarshal(response.Result.Utf8Json, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["method"] != test.method {
				t.Fatalf("wrong native method: %v", payload)
			}
		})
	}
	for _, test := range []struct {
		kind   sessiondv1.RuntimeKind
		legacy string
		branch string
	}{{5, "codex-ssh", "codex_thread_list"}, {4, "ssh", "hermes_projects_tree"}, {7, "grok-ssh", "grok_initialize"}} {
		t.Run(test.legacy, func(t *testing.T) {
			connections := []string{}
			multiplex := core.NewMultiplexRegistry(func(id string, settings map[string]any) (core.Adapter, error) {
				connections = append(connections, id)
				if settings["host"] != "synthetic.invalid" || settings["port"] != 2222 || settings["display_name"] != "fixture" {
					t.Fatalf("remote configuration altered: %v", settings)
				}
				return echoNativeQuery{}, nil
			})
			f.daemon.RegisterRuntime(test.legacy, multiplex)
			for _, id := range []string{"a", "b", "a"} {
				request := &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: test.kind, ConnectionId: ptr(id), Ssh: &sessiondv1.SshConnectConfig{Host: "synthetic.invalid", Port: 2222, DisplayName: ptr("fixture")}}}
				message := request.ProtoReflect()
				branch := message.Descriptor().Fields().ByName(protoreflect.Name(test.branch))
				message.Set(branch, message.NewField(branch))
				if _, err := client.QueryNative(f.ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Join(connections, ",") != "a,b" {
				t.Fatalf("wrong child reuse: %v", connections)
			}
		})
	}
}

func TestQueryNativeRejectsUnknownAndMalformedBeforeRuntime(t *testing.T) {
	f := startFixture(t)
	q := &waitingNativeQuery{result: map[string]any{}}
	f.daemon.RegisterRuntime("hermes", q)
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	for _, mutation := range []func(*sessiondv1.NativeQueryRequest){
		func(r *sessiondv1.NativeQueryRequest) { r.Target.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
		func(r *sessiondv1.NativeQueryRequest) {
			r.GetHermesModelsList().Options = &sessiondv1.NativeJson{Utf8Json: []byte(`{} {}`)}
		},
		func(r *sessiondv1.NativeQueryRequest) {
			r.TopLevelPayload = &sessiondv1.NativeJson{Utf8Json: []byte{0xff}}
		},
		func(r *sessiondv1.NativeQueryRequest) { r.Target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX },
	} {
		r := simpleNativeQuery()
		mutation(r)
		if _, err := client.QueryNative(f.ctx, r); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid query accepted: %v", err)
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("invalid query dispatched")
	}
}
