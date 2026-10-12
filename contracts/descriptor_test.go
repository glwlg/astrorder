package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type actionMap struct {
	SchemaVersion         int               `json:"schema_version"`
	SourceRevision        string            `json:"source_revision"`
	Canonicalization      string            `json:"canonicalization"`
	Baseline              baselineEntry     `json:"baseline"`
	EvidenceSources       []string          `json:"evidence_sources"`
	RequestActions        []actionEntry     `json:"request_actions"`
	ResponseSymbols       []symbolEntry     `json:"response_symbols"`
	EventSymbols          []symbolEntry     `json:"event_symbols"`
	LegacyAliases         []aliasEntry      `json:"legacy_aliases"`
	RuntimeMethods        []runtimeEntry    `json:"runtime_methods"`
	AppDirectCapabilities []capabilityEntry `json:"app_direct_capabilities"`
}

type baselineEntry struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	FreezeStatus string `json:"freeze_status"`
}

type actionEntry struct {
	LegacyAction     string   `json:"legacy_action"`
	RPC              string   `json:"rpc"`
	Disposition      string   `json:"disposition"`
	AliasOf          string   `json:"alias_of,omitempty"`
	RuntimeSupport   []string `json:"runtime_support,omitempty"`
	Mutation         bool     `json:"mutation"`
	OperationPolicy  string   `json:"operation_policy"`
	CanonicalVersion string   `json:"canonical_version,omitempty"`
}

type symbolEntry struct {
	Symbol      string `json:"symbol"`
	Disposition string `json:"disposition"`
}

type aliasEntry struct {
	LegacyName  string `json:"legacy_name"`
	RPC         string `json:"rpc"`
	Namespace   string `json:"namespace"`
	AliasOf     string `json:"alias_of"`
	Disposition string `json:"disposition"`
}

type runtimeEntry struct {
	Runtime     string `json:"runtime"`
	Method      string `json:"method"`
	OneofBranch string `json:"oneof_branch"`
	Disposition string `json:"disposition"`
}

type capabilityEntry struct {
	Capability  string `json:"capability"`
	RPC         string `json:"rpc,omitempty"`
	Disposition string `json:"disposition"`
}

func loadMap(t *testing.T) actionMap {
	t.Helper()
	raw, err := os.ReadFile("action-map.json")
	if err != nil {
		t.Fatalf("read action-map.json: %v", err)
	}
	var got actionMap
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode action-map.json: %v", err)
	}
	return got
}

func TestFullServiceDescriptor(t *testing.T) {
	want := map[protoreflect.FullName][]protoreflect.Name{
		"astrorder.sessiond.v1.DaemonControl":    {"GetCapabilities", "GetStatus", "Shutdown"},
		"astrorder.sessiond.v1.SessionControl":   {"CreateSession", "AttachSession", "Send", "Steer", "StartReview", "Compact", "Interrupt", "RespondApproval", "UpdateSettings", "RenameSession", "DeleteSession", "CloseSession", "DetachSession", "ReportObservedStatus", "SubmitDesktop", "UpdateDesktopSettings"},
		"astrorder.sessiond.v1.RuntimeQuery":     {"QueryNative", "GetHistoryPage", "ListModels", "ListCommands", "ReadModel", "SetModel", "SetReasoning", "ReadApprovalMode", "SetApprovalMode", "DisconnectRuntime"},
		"astrorder.sessiond.v1.EventStream":      {"Subscribe", "AppendEvent"},
		"astrorder.sessiond.v1.TerminalStream":   {"Open"},
		"astrorder.sessiond.v1.OperationQuery":   {"GetOperation"},
		"astrorder.sessiond.v1.ModelConfig":      {"Plan", "Apply", "Reload"},
		"astrorder.sessiond.v1.ConnectorIngress": {"Connect", "BindControlSession", "DisconnectConnector"},
	}
	for serviceName, methodNames := range want {
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
		if err != nil {
			t.Fatalf("missing service %s: %v", serviceName, err)
		}
		service, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			t.Fatalf("%s is %T, want service", serviceName, desc)
		}
		got := make([]string, 0, service.Methods().Len())
		for i := 0; i < service.Methods().Len(); i++ {
			got = append(got, string(service.Methods().Get(i).Name()))
		}
		wantNames := make([]string, len(methodNames))
		for i, name := range methodNames {
			wantNames[i] = string(name)
		}
		if strings.Join(got, ",") != strings.Join(wantNames, ",") {
			t.Fatalf("%s methods = %v, want %v", serviceName, got, wantNames)
		}
	}
}

func TestActionMapSeparatesNamespacesAndCoversFrozenRequests(t *testing.T) {
	got := loadMap(t)
	if got.SchemaVersion != 1 || got.SourceRevision == "" || got.Canonicalization != "astrorder-operation-v1" {
		t.Fatalf("invalid map header: %+v", got)
	}
	if got.Baseline.Path != "docs/reports/full-go-baseline.json" || len(got.Baseline.SHA256) != 64 || got.Baseline.FreezeStatus != "pending-p0-action-freeze" {
		t.Fatalf("invalid baseline provenance: %+v", got.Baseline)
	}
	if len(got.EvidenceSources) < 20 {
		t.Fatalf("action map lacks dispatcher/caller evidence: %v", got.EvidenceSources)
	}
	seenRPC := map[string]string{}
	for _, entry := range got.RequestActions {
		if entry.LegacyAction == "" || entry.RPC == "" || entry.Disposition == "" || entry.OperationPolicy == "" {
			t.Fatalf("incomplete request mapping: %+v", entry)
		}
		if previous, exists := seenRPC[entry.LegacyAction]; exists {
			t.Fatalf("duplicate request %q (%s, %s)", entry.LegacyAction, previous, entry.RPC)
		}
		seenRPC[entry.LegacyAction] = entry.RPC
		if entry.Mutation && entry.OperationPolicy == "durable-v1" && entry.CanonicalVersion != "astrorder-operation-v1" {
			t.Fatalf("durable mutation %s lacks canonical version", entry.LegacyAction)
		}
	}
	responses := map[string]bool{}
	for _, entry := range got.ResponseSymbols {
		responses[entry.Symbol] = true
	}
	for _, name := range []string{"daemon.handshake.result", "daemon.status.result", "operation.read.result", "session.sync.result"} {
		if !responses[name] {
			t.Errorf("response symbol %q is not classified", name)
		}
		if _, request := seenRPC[name]; request {
			t.Errorf("response symbol %q was incorrectly mapped as a request RPC", name)
		}
	}
	events := map[string]bool{}
	for _, entry := range got.EventSymbols {
		events[entry.Symbol] = true
	}
	for _, name := range []string{"connector.event", "connector.hello", "runtime.owner", "runtime.ownership_lost", "session.event", "session.status"} {
		if !events[name] {
			t.Errorf("event symbol %q is not classified", name)
		}
	}
	aliases := map[string]aliasEntry{}
	for _, entry := range got.LegacyAliases {
		if entry.LegacyName == "" || entry.RPC == "" || entry.Namespace == "" || entry.AliasOf == "" || entry.Disposition == "" {
			t.Fatalf("incomplete legacy alias: %+v", entry)
		}
		aliases[entry.LegacyName] = entry
	}
	for _, name := range []string{"approval.respond", "session.approval.respond"} {
		entry, ok := aliases[name]
		if !ok || entry.RPC != "astrorder.sessiond.v1.SessionControl/RespondApproval" || entry.AliasOf != "session.approve" {
			t.Fatalf("approval alias %q = %+v", name, entry)
		}
	}
}

func TestRuntimeQueryInventoryIsClosedAndDesktopIsMutation(t *testing.T) {
	got := loadMap(t)
	want := []string{
		"codex:account/read", "codex:hooks/list", "codex:initialize", "codex:model/list", "codex:skills/list", "codex:thread/items/list", "codex:thread/list", "codex:thread/loaded/list", "codex:thread/read",
		"grok:initialize",
		"hermes:commands.list", "hermes:models.list", "hermes:projects.project_sessions", "hermes:projects.tree", "hermes:session.active_list", "hermes:session.history", "hermes:session.list",
	}
	actual := make([]string, 0, len(got.RuntimeMethods))
	for _, entry := range got.RuntimeMethods {
		if entry.OneofBranch == "" || entry.Disposition == "" {
			t.Fatalf("incomplete runtime method mapping: %+v", entry)
		}
		actual = append(actual, entry.Runtime+":"+entry.Method)
		if strings.HasPrefix(entry.Method, "desktop/") {
			t.Fatalf("desktop mutation leaked into read-only QueryNative: %+v", entry)
		}
	}
	sort.Strings(actual)
	if strings.Join(actual, "\n") != strings.Join(want, "\n") {
		t.Fatalf("runtime method map drift\n got: %v\nwant: %v", actual, want)
	}
	byCapability := map[string]capabilityEntry{}
	for _, entry := range got.AppDirectCapabilities {
		byCapability[entry.Capability] = entry
	}
	for capability, rpc := range map[string]string{
		"codex.desktop/submit":   "astrorder.sessiond.v1.SessionControl/SubmitDesktop",
		"codex.desktop/settings": "astrorder.sessiond.v1.SessionControl/UpdateDesktopSettings",
	} {
		entry, ok := byCapability[capability]
		if !ok || entry.RPC != rpc || entry.Disposition != "typed-mutation" {
			t.Fatalf("%s mapping = %+v", capability, entry)
		}
	}
	for _, name := range []protoreflect.FullName{
		"astrorder.sessiond.v1.ReportObservedStatusRequest",
		"astrorder.sessiond.v1.ObservedActiveStatus",
	} {
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(name); err != nil {
			t.Fatalf("missing positive-observation descriptor %s: %v", name, err)
		}
	}
	observedDesc, _ := protoregistry.GlobalFiles.FindDescriptorByName("astrorder.sessiond.v1.ObservedActiveStatus")
	observed := observedDesc.(protoreflect.EnumDescriptor)
	for _, forbidden := range []protoreflect.Name{"OBSERVED_ACTIVE_STATUS_IDLE", "OBSERVED_ACTIVE_STATUS_ERROR"} {
		if observed.Values().ByName(forbidden) != nil {
			t.Fatalf("observer contract permits non-positive status %s", forbidden)
		}
	}
}

func TestNoGenericCallOrStructAndTypedOneofsExist(t *testing.T) {
	var violations []string
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != "astrorder.sessiond.v1" {
			return true
		}
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				if method.Name() == "Call" || method.Name() == "Execute" {
					violations = append(violations, string(method.FullName()))
				}
			}
		}
		messages := file.Messages()
		for i := 0; i < messages.Len(); i++ {
			walkMessage(messages.Get(i), &violations)
		}
		return true
	})
	if len(violations) != 0 {
		t.Fatalf("generic or Struct compatibility surface: %v", violations)
	}
	for name, oneof := range map[protoreflect.FullName]string{
		"astrorder.sessiond.v1.NativeQueryRequest":    "query",
		"astrorder.sessiond.v1.ExactIdentifier":       "value",
		"astrorder.sessiond.v1.EventStreamFrame":      "frame",
		"astrorder.sessiond.v1.TerminalClientFrame":   "frame",
		"astrorder.sessiond.v1.TerminalServerFrame":   "frame",
		"astrorder.sessiond.v1.ConnectorIngressFrame": "frame",
		"astrorder.sessiond.v1.ConnectorIngressAck":   "ack",
	} {
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatalf("missing message %s: %v", name, err)
		}
		message := desc.(protoreflect.MessageDescriptor)
		if message.Oneofs().ByName(protoreflect.Name(oneof)) == nil {
			t.Errorf("%s missing oneof %s", name, oneof)
		}
	}
}

func walkMessage(message protoreflect.MessageDescriptor, violations *[]string) {
	fields := message.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.Message() != nil && field.Message().FullName() == "google.protobuf.Struct" {
			*violations = append(*violations, string(field.FullName()))
		}
	}
	for i := 0; i < message.Messages().Len(); i++ {
		walkMessage(message.Messages().Get(i), violations)
	}
}

func TestProtoSourcesDoNotDefineShellOrCredentialFields(t *testing.T) {
	forbidden := []string{"password", "private_key", "authorization", "bearer_token", "shell", "shell_command"}
	err := filepath.WalkDir("proto", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(raw))
		for _, needle := range forbidden {
			if strings.Contains(lower, needle) {
				t.Errorf("%s contains forbidden compatibility field/token %q", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
