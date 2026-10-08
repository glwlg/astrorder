package ssh

import "errors"

var (
	// ErrInvalidHost is returned when the target host is empty, starts with a dash,
	// or contains disallowed characters.
	ErrInvalidHost = errors.New("ssh: invalid host")

	// ErrInvalidUser is returned when the SSH user begins with a dash or contains
	// invalid characters.
	ErrInvalidUser = errors.New("ssh: invalid user")

	// ErrInvalidPort is returned when the port is outside the valid range 1..65535.
	ErrInvalidPort = errors.New("ssh: invalid port")

	// ErrInvalidIdentityFile is returned when an identity file path begins with a dash
	// or contains null characters.
	ErrInvalidIdentityFile = errors.New("ssh: invalid identity file")

	// ErrDisallowedOption is returned when an option key is not in the safe allowlist
	// or represents a forbidden switch.
	ErrDisallowedOption = errors.New("ssh: disallowed option")

	// ErrInvalidOptionValue is returned when an option value contains disallowed characters.
	ErrInvalidOptionValue = errors.New("ssh: invalid option value")

	// ErrEmptyExecutable is returned when the command spec executable is empty.
	ErrEmptyExecutable = errors.New("ssh: executable must not be empty")

	// ErrInvalidExecutable is returned when the command spec executable begins with a dash.
	ErrInvalidExecutable = errors.New("ssh: invalid executable name or path")

	// ErrCredentialInArgv is returned when credentials or secrets are detected in argv.
	ErrCredentialInArgv = errors.New("ssh: credentials must not be passed in argv")

	// ErrSessionClosed is returned when an operation is attempted on a locally closed session.
	ErrSessionClosed = errors.New("ssh: session closed locally")
)
