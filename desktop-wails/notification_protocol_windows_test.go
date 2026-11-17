package main

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestWindowsNotificationProtocolKeepsInstallationAndGroupTarget(t *testing.T) {
	state := t.TempDir()
	backend, _ := url.Parse("http://127.0.0.1:30102")
	service := &ShellService{config: Config{StateDir: state, Backend: backend}}
	root := `Software\Classes\` + notificationScheme(state)
	t.Cleanup(func() {
		for _, suffix := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ""} {
			if err := registry.DeleteKey(registry.CURRENT_USER, root+suffix); err != nil {
				t.Errorf("remove isolated test protocol: %v", err)
			}
		}
	})
	launch, err := service.notificationLaunchURL("/groups/group-check")
	if err != nil {
		t.Fatal(err)
	}
	path, err := notificationPathFromURL(launch, state)
	if err != nil || path != "/groups/group-check" {
		t.Fatal(path, err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, root+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	command, _, err := key.GetStringValue("")
	if err != nil || !strings.Contains(command, state) || !strings.Contains(command, "--packaged=false") || !strings.Contains(command, backend.String()) || !strings.HasSuffix(command, `--notification "%1"`) {
		t.Fatalf("invalid protocol command: %q %v", command, err)
	}
}
