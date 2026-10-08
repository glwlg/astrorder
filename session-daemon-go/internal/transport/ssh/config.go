package ssh

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config specifies the connection parameters for an OpenSSH transport.
type Config struct {
	// Host is the remote hostname or IP address (required).
	Host string

	// User is the optional remote user name.
	User string

	// Port is the optional SSH port (1..65535). Defaults to system OpenSSH default (22).
	Port int

	// IdentityFile is the optional path to a private key file.
	IdentityFile string

	// Binary is the ssh client executable name or path. Defaults to "ssh".
	Binary string

	// ConnectTimeout is the connection timeout passed to ssh via -o ConnectTimeout.
	ConnectTimeout time.Duration

	// Options contains strictly allowlisted OpenSSH options.
	// Arbitrary caller-supplied switches are disallowed.
	Options map[string]string

	// MaxStderrBytes is the maximum number of stderr bytes to buffer. Defaults to 64KB.
	MaxStderrBytes int

	// MockEnv is used for testing subprocess trees without touching production.
	MockEnv []string
}

var allowedOptionKeys = map[string]bool{
	"BatchMode":           true,
	"ConnectTimeout":      true,
	"ServerAliveInterval": true,
	"ServerAliveCountMax": true,
	"TCPKeepAlive":         true,
	"Compression":          true,
	"LogLevel":             true,
}

// Validate verifies that the config contains safe parameters and forbids switch injection.
func (c Config) Validate() error {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return ErrInvalidHost
	}
	if strings.HasPrefix(host, "-") {
		return ErrInvalidHost
	}
	// Reject characters that could be misinterpreted by shells or option parsers
	if strings.ContainsAny(host, " \t\r\n;`$&|<>()\"'\\@") {
		return ErrInvalidHost
	}

	if c.User != "" {
		if strings.HasPrefix(c.User, "-") {
			return ErrInvalidUser
		}
		if strings.ContainsAny(c.User, " \t\r\n;`$&|<>()\"'\\@") {
			return ErrInvalidUser
		}
	}

	if c.Port < 0 || c.Port > 65535 {
		return ErrInvalidPort
	}

	if c.IdentityFile != "" {
		if strings.HasPrefix(c.IdentityFile, "-") {
			return ErrInvalidIdentityFile
		}
		if strings.ContainsRune(c.IdentityFile, 0) || strings.ContainsAny(c.IdentityFile, "\r\n") {
			return ErrInvalidIdentityFile
		}
	}

	if c.Binary != "" && strings.HasPrefix(c.Binary, "-") {
		return fmt.Errorf("ssh: binary path must not start with a dash")
	}

	// Check strictly allowlisted options
	for k, v := range c.Options {
		if !allowedOptionKeys[k] {
			return fmt.Errorf("%w: %s", ErrDisallowedOption, k)
		}
		if strings.ContainsAny(v, " \t\r\n;`$&|<>()\"'\\") {
			return fmt.Errorf("%w: invalid characters in value for %s", ErrInvalidOptionValue, k)
		}
		switch k {
		case "BatchMode", "TCPKeepAlive", "Compression":
			if v != "yes" && v != "no" {
				return fmt.Errorf("%w: %s must be yes or no", ErrInvalidOptionValue, k)
			}
		case "ConnectTimeout", "ServerAliveInterval", "ServerAliveCountMax":
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				return fmt.Errorf("%w: %s must be a non-negative integer", ErrInvalidOptionValue, k)
			}
		case "LogLevel":
			upper := strings.ToUpper(v)
			switch upper {
			case "QUIET", "FATAL", "ERROR", "INFO", "VERBOSE", "DEBUG", "DEBUG1", "DEBUG2", "DEBUG3":
			default:
				return fmt.Errorf("%w: invalid log level %s", ErrInvalidOptionValue, v)
			}
		}
	}

	return nil
}

// BuildArgv constructs the argv slice for executing OpenSSH.
// It strictly preserves OpenSSH system defaults while ensuring that no switches
// can be injected via host, user, port, identity, or options.
func (c Config) BuildArgv(remoteCommand string) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	binary := c.Binary
	if binary == "" {
		binary = "ssh"
	}

	argv := []string{binary}

	if c.Port > 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}

	if c.IdentityFile != "" {
		argv = append(argv, "-i", filepath.Clean(c.IdentityFile))
	}

	if c.ConnectTimeout > 0 {
		seconds := int(c.ConnectTimeout.Seconds())
		if seconds <= 0 {
			seconds = 1
		}
		argv = append(argv, "-o", fmt.Sprintf("ConnectTimeout=%d", seconds))
	}

	if len(c.Options) > 0 {
		keys := make([]string, 0, len(c.Options))
		for k := range c.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			argv = append(argv, "-o", fmt.Sprintf("%s=%s", k, c.Options[k]))
		}
	}

	// End of options marker prevents any leading dashes in destination from being parsed as options
	argv = append(argv, "--")

	destination := c.Host
	if c.User != "" {
		destination = c.User + "@" + c.Host
	}
	argv = append(argv, destination, remoteCommand)

	return argv, nil
}
