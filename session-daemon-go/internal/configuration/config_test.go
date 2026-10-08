package configuration

import (
	"astrorder.dev/session-daemon/internal/protocol"
	"strings"
	"testing"
)

func TestConfigurationRegistersHermesWithoutStartingGateway(t *testing.T) {
	root := t.TempDir()
	config := Config{Runtimes: []Runtime{{Type: "hermes", Executable: "hermes", Workspace: root, Allowed: []string{root}}}}
	d := protocol.New("isolated")
	defer d.Close()
	if err := config.Register(d); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationRegistersOnlyValidatedLocalRuntimes(t *testing.T) {
	root := t.TempDir()
	raw := `{"runtimes":[{"type":"codex","executable":"codex","workspace":"ROOT","allowed":["ROOT"]},{"type":"pty","allowed":["ROOT"]}]}`
	root = strings.ReplaceAll(root, `\`, `\\`)
	config, err := Parse(strings.NewReader(strings.ReplaceAll(raw, "ROOT", root)))
	if err != nil {
		t.Fatal(err)
	}
	d := protocol.New("key")
	if err = config.Register(d); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"secret":"forbidden"}`, `{"runtimes":[{"type":"fake"}]}`, `{"runtimes":[{"type":"codex","executable":"codex","workspace":"missing","allowed":[]}]}`} {
		config, err := Parse(strings.NewReader(raw))
		if err == nil {
			err = config.Register(d)
		}
		if err == nil {
			t.Fatal("invalid config accepted", raw)
		}
	}
}
