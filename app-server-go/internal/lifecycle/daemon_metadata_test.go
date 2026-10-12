package lifecycle

import (
	"context"
	"os"
	"testing"
)

func TestDaemonStatusDoesNotReadAppProcessIdentity(t *testing.T) {
	state := t.TempDir()
	identity, err := InspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeProcessMetadata(state, identity); err != nil {
		t.Fatal(err)
	}
	port := mustPort(t, unusedLoopbackAddress(t))
	status, err := daemonStatus(context.Background(), validatedDaemonOptions{DaemonOptions: DaemonOptions{StateDir: state, Port: port, ExecutablePath: os.Args[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateStopped || status.Process.PID != 0 {
		t.Fatalf("daemon read App metadata: %+v", status)
	}
}
