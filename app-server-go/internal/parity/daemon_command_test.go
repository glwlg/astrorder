package parity

import (
	"reflect"
	"testing"
)

func TestDaemonTestEvidenceUsesFixedModuleCommand(t *testing.T) {
	test := TestRef{
		Path:      "session-daemon-go/internal/grpcapi/operation_test.go",
		Name:      "TestGetOperationPreservesUntypedAndMalformedCompletedBytes",
		CommandID: "go-daemon-package-test",
		Command:   "cd session-daemon-go && go test ./internal/grpcapi -count=1",
	}
	command, err := ResolveTestCommand(test)
	if err != nil {
		t.Fatal(err)
	}
	if command.WorkingDir != "session-daemon-go" || !command.FinalEligible ||
		!reflect.DeepEqual(command.Argv, []string{"go", "test", "./internal/grpcapi", "-count=1"}) {
		t.Fatalf("daemon evidence resolved incorrectly: %+v", command)
	}
	for _, mutate := range []func(*TestRef){
		func(ref *TestRef) { ref.Path = "app-server-go/internal/grpcapi/operation_test.go" },
		func(ref *TestRef) { ref.Path = "session-daemon-go/../backend/test.py" },
		func(ref *TestRef) { ref.Command += " && stop-daemon" },
		func(ref *TestRef) { ref.Command = "cd session-daemon-go && go test ./... -count=1" },
		func(ref *TestRef) { ref.Name = "TestX; stop-daemon" },
	} {
		invalid := test
		mutate(&invalid)
		if _, err := ResolveTestCommand(invalid); err == nil {
			t.Fatalf("unsafe daemon evidence command accepted: %+v", invalid)
		}
	}
}
