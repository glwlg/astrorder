package process

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedCommandTerminatesDescendantTree(t *testing.T) {
	mode := os.Getenv("ASTRORDER_TREE_TEST_MODE")
	if mode == "leaf" {
		for {
			os.WriteFile(os.Getenv("ASTRORDER_TREE_TEST_MARKER"), []byte("running"), 0600)
			time.Sleep(10 * time.Millisecond)
		}
	}
	if mode == "leader" {
		child := exec.Command(os.Args[0], "-test.run=TestOwnedCommandTerminatesDescendantTree")
		child.Env = append(os.Environ(), "ASTRORDER_TREE_TEST_MODE=leaf")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, child.Process.Pid)
		for {
			time.Sleep(time.Second)
		}
	}
	marker := filepath.Join(t.TempDir(), "heartbeat")
	cmd := exec.Command(os.Args[0], "-test.run=TestOwnedCommandTerminatesDescendantTree")
	cmd.Env = append(os.Environ(), "ASTRORDER_TREE_TEST_MODE=leader", "ASTRORDER_TREE_TEST_MARKER="+marker)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := StartOwnedCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	defer cmd.Wait()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line == "" {
		t.Fatal("descendant did not start", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant heartbeat missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	// Exact marker bytes prove no descendant remains executing after cleanup;
	// kill(pid,0) alone can report Linux zombies as alive.
	if err = os.WriteFile(marker, []byte("stopped"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	bytes, err := os.ReadFile(marker)
	if err != nil || string(bytes) != "stopped" {
		t.Fatal("descendant survived owned cleanup", err)
	}
}
