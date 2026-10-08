package ssh

import (
	"errors"
	"strings"
)

// CommandSpec defines a remote command to be executed over SSH.
type CommandSpec struct {
	// Environment is an allowlisted provider-only stdin bootstrap, never argv.
	Environment map[string]string
	// Executable is the binary or script path to execute on the remote host.
	Executable string
	// Args are the positional arguments passed to the executable.
	Args []string
	// Dir is the optional remote directory to change to before execution.
	Dir string
}

// Validate checks the CommandSpec for safety and sanity, ensuring that
// no switch injection occurs and credentials are not passed in argv.
func (s CommandSpec) Validate() error {
	if strings.TrimSpace(s.Executable) == "" {
		return ErrEmptyExecutable
	}
	if strings.HasPrefix(s.Executable, "-") {
		return ErrInvalidExecutable
	}
	if strings.ContainsRune(s.Executable, 0) {
		return ErrInvalidExecutable
	}
	if strings.ContainsRune(s.Dir, 0) {
		return errors.New("ssh: working directory contains null byte")
	}

	// Enforce: DO NOT put env credentials in argv
	for _, arg := range s.Args {
		if strings.Contains(arg, "ASTRORDER_SESSION_DAEMON_SECRET") {
			return ErrCredentialInArgv
		}
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "--token=") ||
			strings.HasPrefix(lower, "--secret=") ||
			strings.HasPrefix(lower, "--password=") ||
			strings.HasPrefix(lower, "--api-key=") ||
			strings.HasPrefix(lower, "--api_key=") {
			return ErrCredentialInArgv
		}
		if strings.Contains(arg, "=") {
			key, _, _ := strings.Cut(arg, "=")
			upperKey := strings.ToUpper(key)
			if strings.Contains(upperKey, "SECRET") ||
				strings.Contains(upperKey, "TOKEN") ||
				strings.Contains(upperKey, "PASSWORD") ||
				strings.Contains(upperKey, "API_KEY") ||
				strings.Contains(upperKey, "AUTH") ||
				strings.Contains(upperKey, "PASSWD") {
				return ErrCredentialInArgv
			}
		}
	}
	return nil
}

// BuildRemoteCommand serializes the command specification into a POSIX-quoted
// shell command suitable for remote execution.
func BuildRemoteCommand(spec CommandSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}

	prefix, err := environmentCommand(spec.Environment)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(prefix)
	if spec.Dir != "" {
		b.WriteString("cd -- ")
		b.WriteString(Quote(spec.Dir))
		b.WriteString(" && ")
	}
	b.WriteString("exec ")
	b.WriteString(Quote(spec.Executable))
	for _, arg := range spec.Args {
		b.WriteString(" ")
		b.WriteString(Quote(arg))
	}
	return b.String(), nil
}
