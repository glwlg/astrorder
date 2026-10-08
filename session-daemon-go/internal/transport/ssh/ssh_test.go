package ssh_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestMain(m *testing.M) {
	role := os.Getenv("ASTRORDER_SSH_TEST_ROLE")
	if role != "" {
		switch role {
		case "echo":
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				line := scanner.Text()
				if line == "EXIT" {
					break
				}
				fmt.Fprintf(os.Stdout, "ECHO:%s\n", line)
				fmt.Fprintf(os.Stderr, "LOG:%s\n", line)
			}
			os.Exit(0)
		case "exit_code_42":
			fmt.Fprintln(os.Stderr, "exiting with code 42")
			os.Exit(42)
		case "sleep_forever":
			fmt.Fprintln(os.Stdout, "READY")
			for {
				time.Sleep(100 * time.Millisecond)
			}
		case "spam_stderr":
			for i := 0; i < 2000; i++ {
				fmt.Fprintln(os.Stderr, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
			}
			os.Exit(0)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestPOSIXShellQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "simple word",
			input:    "hello",
			expected: "'hello'",
		},
		{
			name:     "spaces",
			input:    "hello world",
			expected: "'hello world'",
		},
		{
			name:     "single quote inside",
			input:    "foo'bar",
			expected: "'foo'\\''bar'",
		},
		{
			name:     "double quotes inside",
			input:    "foo\"bar\"",
			expected: "'foo\"bar\"'",
		},
		{
			name:     "dollar sign and backticks",
			input:    "$PATH `whoami` $(id)",
			expected: "'$PATH `whoami` $(id)'",
		},
		{
			name:     "newline inside",
			input:    "line1\nline2",
			expected: "'line1\nline2'",
		},
		{
			name:     "unicode and emojis",
			input:    "你好，世界 🚀 星序",
			expected: "'你好，世界 🚀 星序'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := ssh.Quote(tc.input)
			if actual != tc.expected {
				t.Fatalf("Quote(%q) = %q; want %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestBuildRemoteCommand(t *testing.T) {
	t.Run("without dir", func(t *testing.T) {
		spec := ssh.CommandSpec{
			Executable: "codex",
			Args:       []string{"app-server", "--listen", "stdio://"},
		}
		cmd, err := ssh.BuildRemoteCommand(spec)
		if err != nil {
			t.Fatal(err)
		}
		expected := "exec 'codex' 'app-server' '--listen' 'stdio://'"
		if cmd != expected {
			t.Fatalf("got %q, want %q", cmd, expected)
		}
	})

	t.Run("with dir and spaces and quotes and unicode", func(t *testing.T) {
		spec := ssh.CommandSpec{
			Dir:        "/tmp/work space/测试'dir",
			Executable: "/opt/bin/agent 🚀",
			Args:       []string{"arg 1", "arg'2", "$VAR"},
		}
		cmd, err := ssh.BuildRemoteCommand(spec)
		if err != nil {
			t.Fatal(err)
		}
		expected := "cd -- '/tmp/work space/测试'\\''dir' && exec '/opt/bin/agent 🚀' 'arg 1' 'arg'\\''2' '$VAR'"
		if cmd != expected {
			t.Fatalf("got %q, want %q", cmd, expected)
		}
	})
}

func TestCommandSpecRejectsCredentialsInArgv(t *testing.T) {
	tests := []struct {
		name string
		spec ssh.CommandSpec
	}{
		{
			name: "secret token in args",
			spec: ssh.CommandSpec{
				Executable: "codex",
				Args:       []string{"--token=super_secret_token"},
			},
		},
		{
			name: "daemon secret in args",
			spec: ssh.CommandSpec{
				Executable: "codex",
				Args:       []string{"ASTRORDER_SESSION_DAEMON_SECRET=12345"},
			},
		},
		{
			name: "api key in args",
			spec: ssh.CommandSpec{
				Executable: "codex",
				Args:       []string{"OPENAI_API_KEY=sk-test1234"},
			},
		},
		{
			name: "empty executable",
			spec: ssh.CommandSpec{
				Executable: "",
			},
		},
		{
			name: "flag executable",
			spec: ssh.CommandSpec{
				Executable: "-rm",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ssh.BuildRemoteCommand(tc.spec)
			if err == nil {
				t.Fatalf("expected error for spec %+v, got nil", tc.spec)
			}
		})
	}
}

func TestConfigValidationAndSafeArgv(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		cfg := ssh.Config{
			Host:           "192.168.1.100",
			User:           "luwei",
			Port:           22,
			IdentityFile:   "/home/luwei/.ssh/id_ed25519",
			ConnectTimeout: 5 * time.Second,
			Options: map[string]string{
				"BatchMode": "yes",
			},
		}
		argv, err := cfg.BuildArgv("exec 'true'")
		if err != nil {
			t.Fatal(err)
		}

		var foundSeparator bool
		var destIndex int
		for i, arg := range argv {
			if arg == "--" {
				foundSeparator = true
				destIndex = i + 1
				break
			}
		}
		if !foundSeparator {
			t.Fatal("expected '--' separator in argv")
		}
		if destIndex >= len(argv) || argv[destIndex] != "luwei@192.168.1.100" {
			t.Fatalf("expected destination after '--', got %v", argv)
		}
	})

	t.Run("rejects flag host switch injection", func(t *testing.T) {
		cfg := ssh.Config{
			Host: "-oProxyCommand=touch /tmp/pwned",
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for flag-like host")
		}
	})

	t.Run("rejects flag user switch injection", func(t *testing.T) {
		cfg := ssh.Config{
			Host: "192.168.1.100",
			User: "-F/etc/shadow",
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for flag-like user")
		}
	})

	t.Run("rejects invalid ports", func(t *testing.T) {
		for _, port := range []int{-1, 65536, 100000} {
			cfg := ssh.Config{
				Host: "192.168.1.100",
				Port: port,
			}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected error for port %d", port)
			}
		}
	})

	t.Run("rejects flag identity file", func(t *testing.T) {
		cfg := ssh.Config{
			Host:         "192.168.1.100",
			IdentityFile: "-oProxyCommand=evil",
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for flag-like identity file")
		}
	})

	t.Run("disallows arbitrary caller switches and insecure options", func(t *testing.T) {
		disallowed := []map[string]string{
			{"ProxyCommand": "echo pwn"},
			{"StrictHostKeyChecking": "no"},
			{"UserKnownHostsFile": "/dev/null"},
			{"LocalCommand": "whoami"},
			{"PermitLocalCommand": "yes"},
			{"InjectedOption": "value"},
		}

		for _, opts := range disallowed {
			cfg := ssh.Config{
				Host:    "192.168.1.100",
				Options: opts,
			}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected rejection of options: %+v", opts)
			}
		}
	})
}

func TestSessionFullDuplexStreaming(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	transport, err := ssh.New(ssh.Config{
		Host:   "localhost",
		Binary: self,
		MockEnv: []string{
			"ASTRORDER_SSH_TEST_ROLE=echo",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := transport.Start(ctx, ssh.CommandSpec{
		Executable: "dummy",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	writer := bufio.NewWriter(sess.Stdin())
	reader := bufio.NewReader(sess.Stdout())

	testLine := "hello from test client 🚀"
	if _, err := fmt.Fprintf(writer, "%s\n", testLine); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}

	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(response) != "ECHO:"+testLine {
		t.Fatalf("got %q, want ECHO:%s", response, testLine)
	}

	fmt.Fprintln(writer, "EXIT")
	writer.Flush()

	status, err := sess.Wait()
	if err != nil {
		t.Fatal(err)
	}

	if !status.ConfirmedRemoteExit {
		t.Fatal("expected ConfirmedRemoteExit to be true for clean exit")
	}
	if status.LocalKilled {
		t.Fatal("expected LocalKilled to be false for clean exit")
	}
	if status.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", status.ExitCode)
	}
}

func TestSessionExitCodePropagation(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	transport, err := ssh.New(ssh.Config{
		Host:   "localhost",
		Binary: self,
		MockEnv: []string{
			"ASTRORDER_SSH_TEST_ROLE=exit_code_42",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := transport.Start(ctx, ssh.CommandSpec{
		Executable: "dummy",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	status, _ := sess.Wait()
	if !status.ConfirmedRemoteExit {
		t.Fatal("expected ConfirmedRemoteExit = true for normal subprocess exit")
	}
	if status.LocalKilled {
		t.Fatal("expected LocalKilled = false")
	}
	if status.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", status.ExitCode)
	}
}

func TestSessionBoundedStderr(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	transport, err := ssh.New(ssh.Config{
		Host:           "localhost",
		Binary:         self,
		MaxStderrBytes: 1024,
		MockEnv: []string{
			"ASTRORDER_SSH_TEST_ROLE=spam_stderr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := transport.Start(ctx, ssh.CommandSpec{
		Executable: "dummy",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	_, _ = sess.Wait()
	stderr := sess.Stderr()
	if len(stderr) > 1024 {
		t.Fatalf("stderr exceeded MaxStderrBytes: got %d bytes", len(stderr))
	}
	if len(stderr) == 0 {
		t.Fatal("expected stderr to contain data")
	}
}

func TestSessionLocalKillDistinction(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	transport, err := ssh.New(ssh.Config{
		Host:   "localhost",
		Binary: self,
		MockEnv: []string{
			"ASTRORDER_SSH_TEST_ROLE=sleep_forever",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sess, err := transport.Start(ctx, ssh.CommandSpec{
		Executable: "dummy",
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer sess.Close()

	reader := bufio.NewReader(sess.Stdout())
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		cancel()
		t.Fatalf("failed to read READY line: %v", err)
	}

	cancel()

	status, _ := sess.Wait()
	if !status.LocalKilled {
		t.Fatal("expected LocalKilled = true when killed by context cancellation")
	}
	if status.ConfirmedRemoteExit {
		t.Fatal("expected ConfirmedRemoteExit = false when killed locally")
	}
}

func TestSessionExplicitCloseDistinction(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	transport, err := ssh.New(ssh.Config{
		Host:   "localhost",
		Binary: self,
		MockEnv: []string{
			"ASTRORDER_SSH_TEST_ROLE=sleep_forever",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sess, err := transport.Start(ctx, ssh.CommandSpec{
		Executable: "dummy",
	})
	if err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(sess.Stdout())
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		_ = sess.Close()
		t.Fatalf("failed to read READY line: %v", err)
	}

	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	status, _ := sess.Wait()
	if !status.LocalKilled {
		t.Fatal("expected LocalKilled = true on explicit Close")
	}
	if status.ConfirmedRemoteExit {
		t.Fatal("expected ConfirmedRemoteExit = false on explicit Close")
	}
}

func TestRealSSHReadOnlyOptIn(t *testing.T) {
	if os.Getenv("ASTRORDER_SSH_REAL_TEST") != "1" {
		t.Skip("skipping real SSH test; opt-in with ASTRORDER_SSH_REAL_TEST=1")
	}

	transport, err := ssh.New(ssh.Config{
		Host:           "192.168.1.100",
		User:           "luwei",
		ConnectTimeout: 5 * time.Second,
		Options: map[string]string{
			"BatchMode": "yes",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdout, stderr, status, err := transport.Execute(ctx, ssh.CommandSpec{
		Executable: "uname",
		Args:       []string{"-s"},
	})

	t.Logf("Real SSH result: status=%+v, err=%v, stdout=%q, stderr=%q", status, err, string(stdout), string(stderr))
	if err != nil {
		if strings.Contains(string(stderr), "Bad owner or permissions") ||
			strings.Contains(string(stderr), "Host key verification failed") ||
			errors.Is(err, context.DeadlineExceeded) {
			t.Logf("Recorded known sandbox/host-key blocker for real SSH test: %v (stderr: %s)", err, strings.TrimSpace(string(stderr)))
			return
		}
		t.Fatalf("unexpected real SSH failure: %v, stderr: %s", err, string(stderr))
	}

	if !status.ConfirmedRemoteExit {
		t.Fatalf("expected ConfirmedRemoteExit, got %+v", status)
	}
	if status.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", status.ExitCode)
	}
	if !strings.Contains(string(stdout), "Linux") {
		t.Fatalf("unexpected stdout: %q", string(stdout))
	}
}
