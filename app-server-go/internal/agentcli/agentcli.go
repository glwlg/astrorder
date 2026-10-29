// Package agentcli implements the public astrorder-agent command as a thin,
// single-attempt HTTP client. Run is reusable by a future astrorderctl adapter.
package agentcli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/lifecycle"
)

const (
	programName           = "astrorder-agent"
	defaultBaseURL        = "http://127.0.0.1:30001"
	invokePath            = "/api/v1/agent/invoke"
	defaultRequestTimeout = 60 * time.Second
	browserRequestTimeout = 30 * time.Minute
	defaultMaxBodyBytes   = int64(16_000_000)
	defaultMaxErrorBytes  = int64(64 << 10)
)

// EnvLookup matches os.LookupEnv and makes command configuration injectable.
type EnvLookup func(string) (string, bool)

// Config contains bounded transport and filesystem dependencies for tests and
// embedding. Zero values select the production defaults.
type Config struct {
	HTTPClient        *http.Client
	ReadFile          func(string) ([]byte, error)
	OpenEnvironment   func(string) (map[string]string, error)
	RequestTimeout    time.Duration
	MaxBodyBytes      int64
	MaxErrorBodyBytes int64
}

// OptionRegistration describes one positional or named command option.
type OptionRegistration struct {
	Name       string   `json:"name"`
	Flags      []string `json:"flags,omitempty"`
	Metavar    string   `json:"metavar,omitempty"`
	Type       string   `json:"type"`
	Positional bool     `json:"positional"`
	Required   bool     `json:"required"`
	HasDefault bool     `json:"has_default"`
	Default    any      `json:"default,omitempty"`
	Help       string   `json:"help,omitempty"`
}

// Registration is command-schema metadata derived from the definitions used
// by the parser itself.
type Registration struct {
	Program string               `json:"program"`
	Command string               `json:"command"`
	Summary string               `json:"summary,omitempty"`
	Options []OptionRegistration `json:"options"`
}

type valueKind string

const (
	kindString  valueKind = "string"
	kindInteger valueKind = "integer"
	kindJSON    valueKind = "json_object"
	kindHelp    valueKind = "help"
)

type optionDefinition struct {
	name         string
	flags        []string
	metavar      string
	kind         valueKind
	positional   bool
	required     bool
	hasDefault   bool
	defaultValue any
	help         string
}

type commandDefinition struct {
	name         string
	help         string
	children     []*commandDefinition
	options      []optionDefinition
	capability   func(map[string]any, Config) (string, map[string]any, error)
	handleResult func(json.RawMessage, map[string]any, Config) (any, error)
}

var helpOption = optionDefinition{
	name: "help", flags: []string{"-h", "--help"}, kind: kindHelp,
	help: "show this help message and exit",
}

var commandRoot = buildCommands()

func buildCommands() *commandDefinition {
	simple := func(name, help, capability string) *commandDefinition {
		return &commandDefinition{
			name: name,
			help: help,
			capability: func(map[string]any, Config) (string, map[string]any, error) {
				return capability, map[string]any{}, nil
			},
		}
	}
	catalog := simple("catalog", "List capabilities", "catalog.list")
	invoke := &commandDefinition{
		name: "invoke",
		help: "Call a capability by id",
		options: []optionDefinition{
			{name: "capability", metavar: "capability", kind: kindString, positional: true, required: true},
			{name: "input", flags: []string{"--input"}, metavar: "INPUT", kind: kindJSON, help: "JSON object"},
			{name: "input_file", flags: []string{"--input-file"}, metavar: "INPUT_FILE", kind: kindString, help: "Read JSON object from UTF-8 file"},
		},
		capability: func(values map[string]any, config Config) (string, map[string]any, error) {
			hasInput := values["input"] != nil
			hasInputFile := values["input_file"] != nil
			if hasInput && hasInputFile {
				return "", nil, errors.New("cannot provide both --input and --input-file")
			}
			var payload map[string]any
			if hasInputFile {
				filePath, _ := values["input_file"].(string)
				var err error
				payload, err = readInputFile(filePath, config)
				if err != nil {
					return "", nil, err
				}
			} else if hasInput {
				var err error
				payload, err = decodeObject(values["input"].(string))
				if err != nil {
					if err.Error() == "--input must be a JSON object" {
						return "", nil, err
					}
					return "", nil, fmt.Errorf("invalid --input JSON: %w", err)
				}
			} else {
				payload = make(map[string]any)
			}
			return values["capability"].(string), payload, nil
		},
	}
	sessionsList := simple("list", "List sessions", "sessions.list")
	sessionsSearch := &commandDefinition{
		name: "search",
		help: "Search sessions",
		options: []optionDefinition{
			{name: "q", metavar: "q", kind: kindString, positional: true, required: true},
			{name: "limit", flags: []string{"--limit"}, metavar: "LIMIT", kind: kindInteger},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{"q": values["q"]}
			if limit, ok := values["limit"]; ok && limit != nil {
				payload["limit"] = limit
			}
			return "sessions.search", payload, nil
		},
	}
	sessionsRead := &commandDefinition{
		name: "read",
		help: "Read session messages",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString},
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString},
			{name: "session_id", flags: []string{"--session-id"}, metavar: "SESSION_ID", kind: kindString},
			{name: "before", flags: []string{"--before"}, metavar: "BEFORE", kind: kindString},
			{name: "limit", flags: []string{"--limit"}, metavar: "LIMIT", kind: kindInteger, hasDefault: true, defaultValue: 80},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{"limit": values["limit"]}
			for _, name := range []string{"key", "agent_id", "session_id", "before"} {
				if value, ok := values[name]; ok && value != "" {
					payload[name] = value
				}
			}
			return "sessions.read", payload, nil
		},
	}

	spawnOptions := []optionDefinition{
		{name: "operation_id", flags: []string{"--operation-id"}, metavar: "OPERATION_ID", kind: kindString, required: true, help: "Unique operation ID"},
		{name: "agent_id", flags: []string{"--agent-id", "--agent"}, metavar: "AGENT_ID", kind: kindString, required: true, help: "Target agent ID"},
		{name: "title", flags: []string{"--title"}, metavar: "TITLE", kind: kindString, help: "Session title"},
		{name: "workspace", flags: []string{"--workspace"}, metavar: "WORKSPACE", kind: kindString, help: "Target workspace path"},
		{name: "model", flags: []string{"--model"}, metavar: "MODEL", kind: kindString, help: "Model name"},
		{name: "provider", flags: []string{"--provider"}, metavar: "PROVIDER", kind: kindString, help: "Model provider"},
		{name: "effort", flags: []string{"--effort"}, metavar: "EFFORT", kind: kindString, help: "Reasoning effort level"},
		{name: "prompt", flags: []string{"--prompt"}, metavar: "PROMPT", kind: kindString, help: "Initial prompt text"},
		{name: "prompt_file", flags: []string{"--prompt-file"}, metavar: "PROMPT_FILE", kind: kindString, help: "Read prompt text from UTF-8 file"},
	}

	spawnCapability := func(values map[string]any, config Config) (string, map[string]any, error) {
		promptText, err := resolvePrompt(values, config)
		if err != nil {
			return "", nil, err
		}
		opID, _ := values["operation_id"].(string)
		agentID, _ := values["agent_id"].(string)
		if containsWhitespace(opID) {
			return "", nil, fmt.Errorf("operation_id contains illegal whitespace: %q", opID)
		}
		if containsWhitespace(agentID) {
			return "", nil, fmt.Errorf("agent_id contains illegal whitespace: %q", agentID)
		}
		payload := map[string]any{
			"operation_id": opID,
			"agent_id":     agentID,
		}
		for _, key := range []string{"title", "workspace", "model", "provider", "effort"} {
			if value, ok := values[key]; ok {
				if str, ok := value.(string); ok && str != "" {
					payload[key] = str
				}
			}
		}
		if promptText != "" {
			payload["prompt"] = promptText
		}
		return "sessions.delegate", payload, nil
	}

	sessionsSpawn := &commandDefinition{
		name:       "spawn",
		help:       "Spawn or delegate a task to an agent",
		options:    spawnOptions,
		capability: spawnCapability,
	}
	sessionsDelegate := &commandDefinition{
		name:       "delegate",
		help:       "Spawn or delegate a task to an agent",
		options:    spawnOptions,
		capability: spawnCapability,
	}

	sessionsOperation := &commandDefinition{
		name: "operation",
		help: "Query operation status",
		options: []optionDefinition{
			{name: "operation_id", flags: []string{"--operation-id"}, metavar: "OPERATION_ID", kind: kindString, required: true, help: "Operation ID"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			return "sessions.operation", map[string]any{
				"operation_id": values["operation_id"],
			}, nil
		},
	}

	sessionsSend := &commandDefinition{
		name: "send",
		help: "Send prompt instruction to a session",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString, help: "Session key"},
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString, help: "Agent ID"},
			{name: "session_id", flags: []string{"--session-id"}, metavar: "SESSION_ID", kind: kindString, help: "Session ID"},
			{name: "command_id", flags: []string{"--command-id"}, metavar: "COMMAND_ID", kind: kindString, help: "Client command ID for idempotency"},
			{name: "text", flags: []string{"--text", "--prompt"}, metavar: "TEXT", kind: kindString, help: "Instruction text"},
			{name: "prompt_file", flags: []string{"--prompt-file"}, metavar: "PROMPT_FILE", kind: kindString, help: "Instruction text from UTF-8 file"},
		},
		capability: func(values map[string]any, config Config) (string, map[string]any, error) {
			text, err := resolvePrompt(values, config)
			if err != nil {
				return "", nil, err
			}
			if text == "" {
				if t, ok := values["text"].(string); ok {
					text = t
				}
			}
			payload := map[string]any{}
			for _, name := range []string{"key", "agent_id", "session_id", "command_id"} {
				if value, ok := values[name]; ok && value != "" {
					payload[name] = value
				}
			}
			if text != "" {
				payload["text"] = text
			}
			return "sessions.send", payload, nil
		},
	}

	sessionsStop := &commandDefinition{
		name: "stop",
		help: "Stop or interrupt a running session",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString, help: "Session key"},
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString, help: "Agent ID"},
			{name: "session_id", flags: []string{"--session-id"}, metavar: "SESSION_ID", kind: kindString, help: "Session ID"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{}
			for _, name := range []string{"key", "agent_id", "session_id"} {
				if value, ok := values[name]; ok && value != "" {
					payload[name] = value
				}
			}
			return "sessions.stop", payload, nil
		},
	}

	sessionsDelete := &commandDefinition{
		name: "delete",
		help: "Delete one or more sessions",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString, help: "Session key"},
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString, help: "Agent ID"},
			{name: "session_id", flags: []string{"--session-id"}, metavar: "SESSION_ID", kind: kindString, help: "Session ID"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{}
			for _, name := range []string{"key", "agent_id", "session_id"} {
				if value, ok := values[name]; ok && value != "" {
					payload[name] = value
				}
			}
			return "sessions.delete", payload, nil
		},
	}

	sessionsTree := &commandDefinition{
		name: "tree",
		help: "Query session hierarchy and satellite DAG topology",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString, help: "Root session key"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{}
			if key, ok := values["key"].(string); ok && key != "" {
				payload["key"] = key
			}
			return "sessions.tree", payload, nil
		},
	}

	sessions := &commandDefinition{
		name: "sessions",
		children: []*commandDefinition{
			sessionsList,
			sessionsSearch,
			sessionsRead,
			sessionsSpawn,
			sessionsDelegate,
			sessionsOperation,
			sessionsSend,
			sessionsStop,
			sessionsDelete,
			sessionsTree,
		},
	}

	bind := &commandDefinition{
		name: "bind",
		help: "Write caller configuration file for an agent profile",
		options: []optionDefinition{
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString, required: true, help: "Caller agent ID (e.g. hermes, codex)"},
			{name: "connection_id", flags: []string{"--connection-id"}, metavar: "CONNECTION_ID", kind: kindString, hasDefault: true, defaultValue: "local", help: "Caller connection ID"},
			{name: "profile_name", flags: []string{"--profile-name"}, metavar: "PROFILE_NAME", kind: kindString, hasDefault: true, defaultValue: "default", help: "Caller profile name"},
			{name: "runtime", flags: []string{"--runtime"}, metavar: "RUNTIME", kind: kindString, hasDefault: true, defaultValue: "hermes", help: "Caller runtime"},
			{name: "output", flags: []string{"--output"}, metavar: "OUTPUT_FILE", kind: kindString, required: true, help: "Target caller config JSON file path"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			agentID := values["agent_id"].(string)
			connID := values["connection_id"].(string)
			profName := values["profile_name"].(string)
			runtime := values["runtime"].(string)
			outputPath := values["output"].(string)

			for _, item := range []struct{ label, val string }{
				{"agent_id", agentID},
				{"connection_id", connID},
				{"profile_name", profName},
				{"runtime", runtime},
			} {
				if containsWhitespace(item.val) {
					return "", nil, fmt.Errorf("%s contains illegal whitespace: %q", item.label, item.val)
				}
			}

			cfg := map[string]string{
				"agent_id":      agentID,
				"connection_id": connID,
				"profile_name":  profName,
				"runtime":       runtime,
			}
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return "", nil, fmt.Errorf("marshal caller config: %w", err)
			}
			data = append(data, '\n')
			if err := os.WriteFile(outputPath, data, 0600); err != nil {
				return "", nil, fmt.Errorf("write caller config %s: %w", outputPath, err)
			}
			return "", map[string]any{
				"status": "bound",
				"path":   outputPath,
				"caller": cfg,
			}, nil
		},
	}

	browserEval := &commandDefinition{
		name: "eval",
		help: "Evaluate JavaScript expression via browser CDP",
		options: []optionDefinition{
			{name: "script_file", flags: []string{"--script-file"}, metavar: "SCRIPT_FILE", kind: kindString, required: true, help: "Read JavaScript from UTF-8 file"},
			{name: "session_key", flags: []string{"--session-key", "--key"}, metavar: "SESSION_KEY", kind: kindString, help: "Session key (default from caller)"},
		},
		capability: func(values map[string]any, config Config) (string, map[string]any, error) {
			scriptPath, _ := values["script_file"].(string)
			readFile := config.ReadFile
			if readFile == nil {
				readFile = os.ReadFile
			}
			data, err := readFile(scriptPath)
			if err != nil {
				return "", nil, fmt.Errorf("read --script-file %s: %w", scriptPath, err)
			}
			limit := config.MaxBodyBytes
			if limit <= 0 {
				limit = defaultMaxBodyBytes
			}
			if int64(len(data)) > limit {
				return "", nil, fmt.Errorf("--script-file %s exceeds maximum size of %d bytes", scriptPath, limit)
			}
			if !utf8.Valid(data) {
				return "", nil, fmt.Errorf("--script-file %s contains invalid UTF-8", scriptPath)
			}
			payload := map[string]any{
				"method": "Runtime.evaluate",
				"params": map[string]any{
					"expression":    string(data),
					"awaitPromise":  true,
					"returnByValue": true,
				},
			}
			if sKey, ok := values["session_key"].(string); ok && sKey != "" {
				payload["session_key"] = sKey
			}
			return "browser.cdp", payload, nil
		},
	}

	browserScreenshot := &commandDefinition{
		name: "screenshot",
		help: "Capture browser screenshot",
		options: []optionDefinition{
			{name: "output", flags: []string{"--output", "-o", "--output-file"}, metavar: "OUTPUT_FILE", kind: kindString, help: "Save screenshot image to file"},
			{name: "session_key", flags: []string{"--session-key", "--key"}, metavar: "SESSION_KEY", kind: kindString, help: "Session key (default from caller)"},
		},
		capability: func(values map[string]any, _ Config) (string, map[string]any, error) {
			payload := map[string]any{}
			if sKey, ok := values["session_key"].(string); ok && sKey != "" {
				payload["session_key"] = sKey
			}
			return "browser.screenshot", payload, nil
		},
		handleResult: func(raw json.RawMessage, values map[string]any, _ Config) (any, error) {
			outputPath, _ := values["output"].(string)
			if outputPath == "" {
				return raw, nil
			}
			var snapMap map[string]any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&snapMap); err != nil {
				return nil, fmt.Errorf("decode screenshot response: %w", err)
			}
			b64Str, ok := snapMap["screenshot"].(string)
			if !ok || strings.TrimSpace(b64Str) == "" {
				return nil, errors.New("screenshot response missing screenshot data")
			}
			if idx := strings.Index(b64Str, ";base64,"); idx >= 0 {
				b64Str = b64Str[idx+8:]
			}
			imgBytes, err := base64.StdEncoding.DecodeString(b64Str)
			if err != nil {
				return nil, fmt.Errorf("decode screenshot base64: %w", err)
			}
			if err := os.WriteFile(outputPath, imgBytes, 0644); err != nil {
				return nil, fmt.Errorf("write screenshot to %s: %w", outputPath, err)
			}
			delete(snapMap, "screenshot")
			snapMap["saved_to"] = outputPath
			snapMap["size_bytes"] = len(imgBytes)
			return snapMap, nil
		},
	}

	browser := &commandDefinition{
		name: "browser",
		help: "Control browser",
		children: []*commandDefinition{
			browserEval,
			browserScreenshot,
		},
	}

	return &commandDefinition{
		children: []*commandDefinition{
			catalog,
			invoke,
			sessions,
			browser,
			simple("agents", "", "agents.list"),
			simple("machines", "", "machines.list"),
			simple("projects", "", "projects.list"),
			bind,
		},
	}
}

// Registrations returns a fresh command catalog derived from the same command
// definitions used by Run. It intentionally contains no environment values.
func Registrations() []Registration {
	var result []Registration
	var walk func(*commandDefinition, []string)
	walk = func(command *commandDefinition, parents []string) {
		for _, child := range command.children {
			path := append(append([]string(nil), parents...), child.name)
			options := effectiveOptions(child)
			registered := make([]OptionRegistration, 0, len(options))
			for _, option := range options {
				flags := append([]string(nil), option.flags...)
				registered = append(registered, OptionRegistration{
					Name: option.name, Flags: flags, Metavar: option.metavar, Type: string(option.kind),
					Positional: option.positional, Required: option.required,
					HasDefault: option.hasDefault, Default: option.defaultValue, Help: option.help,
				})
			}
			result = append(result, Registration{
				Program: programName, Command: strings.Join(path, " "),
				Summary: child.help, Options: registered,
			})
			walk(child, path)
		}
	}
	walk(commandRoot, nil)
	return result
}

func effectiveOptions(command *commandDefinition) []optionDefinition {
	result := make([]optionDefinition, 0, len(command.options)+1)
	result = append(result, helpOption)
	result = append(result, command.options...)
	return result
}

type parseResult struct {
	command *commandDefinition
	path    []string
	values  map[string]any
	help    bool
}

type parseFailure struct {
	command *commandDefinition
	path    []string
	message string
}

func (failure *parseFailure) Error() string { return failure.message }

// Run executes the CLI with injected streams and environment lookup. It
// returns a process-style exit code and never closes the supplied streams.
func Run(ctx context.Context, args []string, in io.Reader, out, errWriter io.Writer, envLookup EnvLookup) int {
	if envLookup == nil {
		envLookup = os.LookupEnv
	}
	if executable, err := os.Executable(); err == nil {
		resolved, err := installationEnvironment(envLookup, executable, os.ReadFile)
		if err != nil {
			fmt.Fprintln(errWriter, err)
			return 1
		}
		envLookup = resolved
	}
	return RunWithConfig(ctx, args, in, out, errWriter, envLookup, Config{})
}

// RunWithConfig is Run with injectable bounded transport dependencies.
func RunWithConfig(ctx context.Context, args []string, in io.Reader, out, errWriter io.Writer, envLookup EnvLookup, config Config) int {
	_ = in
	if ctx == nil {
		ctx = context.Background()
	}
	if envLookup == nil {
		envLookup = os.LookupEnv
	}
	parsed, err := parse(args)
	if err != nil {
		failure := err.(*parseFailure)
		writeUsage(errWriter, failure.command, failure.path)
		program := programName
		if len(failure.path) > 0 {
			program += " " + strings.Join(failure.path, " ")
		}
		_, _ = fmt.Fprintf(errWriter, "%s: error: %s\n", program, failure.message)
		return 2
	}
	if parsed.help {
		writeHelp(out, parsed.command, parsed.path)
		return 0
	}
	capability, payload, err := parsed.command.capability(parsed.values, config)
	if err != nil {
		_, _ = fmt.Fprintln(errWriter, err)
		return 1
	}
	if capability == "" {
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			_, _ = fmt.Fprintf(errWriter, "write output: %v\n", err)
			return 1
		}
		return 0
	}
	result, err := invoke(ctx, capability, payload, envLookup, config)
	if err != nil {
		_, _ = fmt.Fprintln(errWriter, err)
		return 1
	}
	var outputData any = result
	if parsed.command.handleResult != nil {
		outputData, err = parsed.command.handleResult(result, parsed.values, config)
		if err != nil {
			_, _ = fmt.Fprintln(errWriter, err)
			return 1
		}
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(outputData); err != nil {
		_, _ = fmt.Fprintf(errWriter, "write output: %v\n", err)
		return 1
	}
	return 0
}

func parse(args []string) (*parseResult, error) {
	command := commandRoot
	path := []string{}
	remaining := args
	for len(command.children) > 0 {
		if len(remaining) > 0 && (remaining[0] == "-h" || remaining[0] == "--help") {
			return &parseResult{command: command, path: path, help: true}, nil
		}
		if len(remaining) == 0 {
			name := "cmd"
			if len(path) > 0 {
				name = path[len(path)-1] + "_cmd"
			}
			return nil, &parseFailure{command: command, path: path, message: "the following arguments are required: " + name}
		}
		name := remaining[0]
		var next *commandDefinition
		for _, child := range command.children {
			if child.name == name {
				next = child
				break
			}
		}
		if next == nil {
			choices := childNames(command)
			destination := "cmd"
			if len(path) > 0 {
				destination = path[len(path)-1] + "_cmd"
			}
			message := fmt.Sprintf("argument %s: invalid choice: %s (choose from %s)", destination, argumentRepr(name), strings.Join(choices, ", "))
			return nil, &parseFailure{command: command, path: path, message: message}
		}
		command = next
		path = append(path, name)
		remaining = remaining[1:]
	}
	if containsHelp(remaining) {
		return &parseResult{command: command, path: path, help: true}, nil
	}
	values, err := parseLeaf(command, remaining)
	if err != nil {
		return nil, &parseFailure{command: command, path: path, message: err.Error()}
	}
	return &parseResult{command: command, path: path, values: values}, nil
}

func containsHelp(args []string) bool {
	for _, argument := range args {
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}

func parseLeaf(command *commandDefinition, args []string) (map[string]any, error) {
	values := make(map[string]any)
	for _, option := range command.options {
		if option.hasDefault {
			values[option.name] = option.defaultValue
		}
	}
	var positionals []string
	endOptions := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" && !endOptions {
			endOptions = true
			continue
		}
		if !endOptions && strings.HasPrefix(argument, "-") {
			flagName := argument
			inlineValue := ""
			hasInlineValue := false
			if split := strings.IndexByte(argument, '='); split >= 0 {
				flagName = argument[:split]
				inlineValue = argument[split+1:]
				hasInlineValue = true
			}
			option := findFlag(command, flagName)
			if option == nil {
				return nil, fmt.Errorf("unrecognized arguments: %s", argument)
			}
			var raw string
			if hasInlineValue {
				raw = inlineValue
			} else {
				index++
				if index >= len(args) {
					return nil, fmt.Errorf("argument %s: expected one argument", flagName)
				}
				raw = args[index]
			}
			value, err := parseValue(*option, raw)
			if err != nil {
				return nil, err
			}
			values[option.name] = value
			continue
		}
		positionals = append(positionals, argument)
	}
	positionalDefinitions := make([]optionDefinition, 0)
	for _, option := range command.options {
		if option.positional {
			positionalDefinitions = append(positionalDefinitions, option)
		}
	}
	if len(positionals) > len(positionalDefinitions) {
		return nil, fmt.Errorf("unrecognized arguments: %s", strings.Join(positionals[len(positionalDefinitions):], " "))
	}
	for index, option := range positionalDefinitions {
		if index >= len(positionals) {
			if option.required {
				return nil, fmt.Errorf("the following arguments are required: %s", option.name)
			}
			continue
		}
		value, err := parseValue(option, positionals[index])
		if err != nil {
			return nil, err
		}
		values[option.name] = value
	}
	for _, option := range command.options {
		if !option.positional && option.required {
			if _, ok := values[option.name]; !ok {
				flagName := option.name
				if len(option.flags) > 0 {
					flagName = option.flags[0]
				}
				return nil, fmt.Errorf("the following arguments are required: %s", flagName)
			}
		}
	}
	return values, nil
}

func findFlag(command *commandDefinition, name string) *optionDefinition {
	for index := range command.options {
		for _, flagName := range command.options[index].flags {
			if flagName == name {
				return &command.options[index]
			}
		}
	}
	return nil
}

func parseValue(option optionDefinition, raw string) (any, error) {
	switch option.kind {
	case kindString:
		return raw, nil
	case kindInteger:
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("argument %s: invalid int value: %s", option.flags[0], argumentRepr(raw))
		}
		return value, nil
	case kindJSON:
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported option type %q", option.kind)
	}
}

func decodeObject(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("--input must be a JSON object")
	}
	return object, nil
}

func readInputFile(filePath string, config Config) (map[string]any, error) {
	readFile := config.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read --input-file %s: %w", filePath, err)
	}
	limit := config.MaxBodyBytes
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("--input-file %s exceeds maximum size of %d bytes", filePath, limit)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("--input-file %s contains invalid UTF-8", filePath)
	}
	// Windows PowerShell 5.1 commonly writes UTF-8 with a BOM. Strip it after
	// validation so the JSON decoder sees a plain object.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid --input-file JSON: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return nil, fmt.Errorf("invalid --input-file JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("--input-file must contain a JSON object")
	}
	return object, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after JSON value")
		}
		return err
	}
	return nil
}

func invoke(ctx context.Context, capability string, payload map[string]any, envLookup EnvLookup, config Config) (json.RawMessage, error) {
	if requiresCaller(capability) {
		caller, err := resolveCaller(envLookup, config)
		if err != nil {
			return nil, fmt.Errorf("caller resolution failed: %w", err)
		}
		if payload == nil {
			payload = make(map[string]any)
		}
		if err := injectCaller(payload, caller); err != nil {
			return nil, fmt.Errorf("caller injection failed: %w", err)
		}
		if strings.HasPrefix(capability, "browser.") {
			if sKey, ok := payload["session_key"].(string); !ok || strings.TrimSpace(sKey) == "" {
				payload["session_key"] = caller.AgentID + "::" + caller.SessionID
			}
		}
		if err := applyCallerScope(capability, payload, caller); err != nil {
			return nil, fmt.Errorf("caller scope failed: %w", err)
		}
	}
	baseURL := defaultBaseURL
	if value, ok := envLookup("ASTRORDER_AGENT_URL"); ok && value != "" {
		baseURL = value
	}
	token, err := resolveToken(envLookup, config, baseURL)
	if err != nil {
		return nil, err
	}
	body, err := marshalRequest(map[string]any{"capability": capability, "input": payload})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	timeout := config.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
		if capability == "browser.run" {
			timeout = browserRequestTimeout
		}
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, strings.TrimRight(baseURL, "/")+invokePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Astrorder agent API request failed: %s", safeTransportError(err))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := clientFor(config.HTTPClient)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Astrorder agent API request failed: %s", safeTransportError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		limit := config.MaxErrorBodyBytes
		if limit <= 0 {
			limit = defaultMaxErrorBytes
		}
		detail, truncated, readErr := readBounded(response.Body, limit)
		if readErr != nil {
			return nil, fmt.Errorf("Astrorder agent API %d: read response body: %v", response.StatusCode, readErr)
		}
		text := strings.ToValidUTF8(string(detail), "\uFFFD")
		if truncated {
			text += "... (truncated)"
		}
		return nil, fmt.Errorf("Astrorder agent API %d: %s", response.StatusCode, text)
	}
	limit := config.MaxBodyBytes
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	raw, truncated, err := readBounded(response.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("Astrorder agent API response: %v", err)
	}
	if truncated {
		return nil, fmt.Errorf("Astrorder agent API response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var validated any
	if err := decoder.Decode(&validated); err != nil {
		return nil, fmt.Errorf("Astrorder agent API returned invalid JSON: %v", err)
	}
	if err := requireEOF(decoder); err != nil {
		return nil, fmt.Errorf("Astrorder agent API returned invalid JSON: %v", err)
	}
	return json.RawMessage(raw), nil
}

func clientFor(source *http.Client) *http.Client {
	if source == nil {
		return &http.Client{CheckRedirect: rejectRedirect}
	}
	copy := *source
	copy.CheckRedirect = rejectRedirect
	return &copy
}

func rejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func isAllowedLocalOrigin(rawURL string, hasCustomOpenEnv bool) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if strings.ToLower(parsed.Scheme) != "http" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	isLoopback := hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1" || hostname == "[::1]"
	if !isLoopback {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return false
	}
	if hasCustomOpenEnv {
		return true
	}
	return port == "30001"
}

func resolveToken(envLookup EnvLookup, config Config, targetURL string) (string, error) {
	for _, key := range []string{"ASTRORDER_AGENT_TOKEN", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET"} {
		if value, ok := envLookup(key); ok && value != "" {
			return value, nil
		}
	}
	path := ".token"
	if value, ok := envLookup("ASTRORDER_TOKEN_FILE"); ok && value != "" {
		path = value
	}
	readFile := config.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	if raw, err := readFile(path); err == nil {
		return strings.TrimSpace(string(raw)), nil
	}

	// DPAPI fallback ONLY for local loopback origin:
	if isAllowedLocalOrigin(targetURL, config.OpenEnvironment != nil) {
		stateDir, _ := envLookup("ASTRORDER_STATE_DIR")
		if stateDir == "" {
			if localAppData, ok := envLookup("LOCALAPPDATA"); ok && localAppData != "" {
				stateDir = filepath.Join(localAppData, "Astrorder")
			}
		}
		if stateDir != "" && filepath.IsAbs(stateDir) {
			openEnv := config.OpenEnvironment
			if openEnv == nil {
				openEnv = lifecycle.OpenEnvironment
			}
			if envMap, err := openEnv(stateDir); err == nil && envMap != nil {
				for _, key := range []string{"ASTRORDER_AGENT_TOKEN", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET"} {
					if val, ok := envMap[key]; ok && val != "" {
						return val, nil
					}
				}
			}
		}
	}

	return "", errors.New("Missing Astrorder agent token (ASTRORDER_AGENT_TOKEN or .token)")
}

func marshalRequest(value any) ([]byte, error) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(body.Bytes(), []byte("\n")), nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	limited := io.LimitReader(reader, limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

func safeTransportError(err error) string {
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		return urlError.Err.Error()
	}
	return err.Error()
}

func childNames(command *commandDefinition) []string {
	names := make([]string, 0, len(command.children))
	for _, child := range command.children {
		names = append(names, child.name)
	}
	return names
}

func argumentRepr(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func writeUsage(writer io.Writer, command *commandDefinition, path []string) {
	commandName := programName
	if len(path) > 0 {
		commandName += " " + strings.Join(path, " ")
	}
	var terms []string
	terms = append(terms, "[-h]")
	if len(command.children) > 0 {
		terms = append(terms, "{"+strings.Join(childNames(command), ",")+"} ...")
	} else {
		for _, positional := range []bool{false, true} {
			for _, option := range command.options {
				if option.positional != positional {
					continue
				}
				label := option.metavar
				if option.positional {
					terms = append(terms, label)
					continue
				}
				term := option.flags[0] + " " + label
				if !option.required {
					term = "[" + term + "]"
				}
				terms = append(terms, term)
			}
		}
	}
	prefix := "usage: " + commandName
	line := prefix
	continuation := strings.Repeat(" ", len(prefix)+1)
	for _, term := range terms {
		if len(line)+1+len(term) > 80 {
			_, _ = fmt.Fprintln(writer, line)
			line = continuation + term
		} else {
			line += " " + term
		}
	}
	_, _ = fmt.Fprintln(writer, line)
}

func writeHelp(writer io.Writer, command *commandDefinition, path []string) {
	writeUsage(writer, command, path)
	_, _ = fmt.Fprintln(writer)
	if len(path) == 0 {
		_, _ = fmt.Fprintln(writer, "Call Astrorder agent APIs")
		_, _ = fmt.Fprintln(writer)
	}
	if len(command.children) > 0 {
		choices := "{" + strings.Join(childNames(command), ",") + "}"
		_, _ = fmt.Fprintln(writer, "positional arguments:")
		_, _ = fmt.Fprintf(writer, "  %s\n", choices)
		for _, child := range command.children {
			if child.help != "" {
				_, _ = fmt.Fprintf(writer, "    %-20s%s\n", child.name, child.help)
			}
		}
		_, _ = fmt.Fprintln(writer)
	} else {
		positionals := make([]optionDefinition, 0)
		for _, option := range command.options {
			if option.positional {
				positionals = append(positionals, option)
			}
		}
		if len(positionals) > 0 {
			_, _ = fmt.Fprintln(writer, "positional arguments:")
			for _, option := range positionals {
				_, _ = fmt.Fprintf(writer, "  %s\n", option.metavar)
			}
			_, _ = fmt.Fprintln(writer)
		}
	}
	_, _ = fmt.Fprintln(writer, "options:")
	named := effectiveOptions(command)
	width := 0
	labels := make(map[string]string)
	for _, option := range named {
		if option.positional {
			continue
		}
		label := strings.Join(option.flags, ", ")
		if option.metavar != "" {
			label += " " + option.metavar
		}
		labels[option.name] = label
		if len(label) > width {
			width = len(label)
		}
	}
	for _, option := range named {
		if option.positional {
			continue
		}
		label := labels[option.name]
		if option.help == "" {
			_, _ = fmt.Fprintf(writer, "  %s\n", label)
		} else {
			_, _ = fmt.Fprintf(writer, "  %-*s  %s\n", width, label, option.help)
		}
	}
}

// Caller represents the unified wire protocol caller identity.
type Caller struct {
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
	Runtime      string `json:"runtime"`
	SessionID    string `json:"session_id"`
}

func containsWhitespace(s string) bool {
	return strings.IndexFunc(s, unicode.IsSpace) >= 0
}

func resolvePrompt(values map[string]any, config Config) (string, error) {
	if pf, ok := values["prompt_file"].(string); ok && pf != "" {
		readFile := config.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		data, err := readFile(pf)
		if err != nil {
			return "", fmt.Errorf("read --prompt-file: %w", err)
		}
		if !utf8.Valid(data) {
			return "", fmt.Errorf("--prompt-file %s contains invalid UTF-8", pf)
		}
		return string(data), nil
	}
	if p, ok := values["prompt"].(string); ok {
		return p, nil
	}
	return "", nil
}

type callerConfigFile struct {
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
	Runtime      string `json:"runtime"`
}

func readCallerConfig(path string, readFile func(string) ([]byte, error)) (*callerConfigFile, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read caller config %s: %w", path, err)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("parse caller config %s: %w", path, err)
	}
	if _, ok := rawMap["session_id"]; ok {
		return nil, fmt.Errorf("caller config %s must not contain session_id", path)
	}
	if _, ok := rawMap["sessionid"]; ok {
		return nil, fmt.Errorf("caller config %s must not contain sessionid", path)
	}

	allowedKeys := map[string]bool{
		"agent_id":      true,
		"connection_id": true,
		"profile_name":  true,
		"runtime":       true,
	}
	for k := range rawMap {
		if !allowedKeys[k] {
			return nil, fmt.Errorf("caller config %s contains unknown field %q", path, k)
		}
	}

	cfg := &callerConfigFile{}
	for _, key := range []string{"agent_id", "connection_id", "profile_name", "runtime"} {
		v, ok := rawMap[key].(string)
		if !ok || v == "" {
			return nil, fmt.Errorf("caller config %s field %q is required and must not be empty", path, key)
		}
		if containsWhitespace(v) {
			return nil, fmt.Errorf("caller config %s field %q contains illegal whitespace: %q", path, key, v)
		}
		switch key {
		case "agent_id":
			cfg.AgentID = v
		case "connection_id":
			cfg.ConnectionID = v
		case "profile_name":
			cfg.ProfileName = v
		case "runtime":
			cfg.Runtime = v
		}
	}
	return cfg, nil
}

func findHermesCallerConfig(envLookup EnvLookup, readFile func(string) ([]byte, error)) (*callerConfigFile, error) {
	home, hasHome := envLookup("HERMES_HOME")
	hasHome = hasHome && home != ""

	var targetPath string
	var isExplicitHome bool
	if hasHome {
		targetPath = filepath.Join(home, "astrorder-caller.json")
		isExplicitHome = true
	} else {
		localAppData, ok := envLookup("LOCALAPPDATA")
		if !ok || localAppData == "" {
			return nil, nil
		}
		targetPath = filepath.Join(localAppData, "hermes", "astrorder-caller.json")
		isExplicitHome = false
	}

	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hermes caller config %s: %w", targetPath, err)
	}

	cfg, err := readCallerConfig(targetPath, func(string) ([]byte, error) { return raw, nil })
	if err != nil {
		return nil, err
	}

	if cfg.Runtime != "hermes" {
		return nil, fmt.Errorf("hermes caller config %s must have runtime \"hermes\", got %q", targetPath, cfg.Runtime)
	}

	if hermesProfile, ok := envLookup("HERMES_PROFILE"); ok && hermesProfile != "" {
		if cfg.ProfileName != hermesProfile {
			return nil, fmt.Errorf("hermes caller config %s profile_name %q does not match HERMES_PROFILE %q", targetPath, cfg.ProfileName, hermesProfile)
		}
	} else if !isExplicitHome {
		if cfg.ProfileName != "default" {
			return nil, fmt.Errorf("hermes caller config in default home %s must have profile_name \"default\", got %q", targetPath, cfg.ProfileName)
		}
	}

	return cfg, nil
}

func resolveCaller(envLookup EnvLookup, config Config) (*Caller, error) {
	configPath, hasConfig := envLookup("ASTRORDER_CALLER_CONFIG")
	if hasConfig && configPath == "" {
		return nil, errors.New("ASTRORDER_CALLER_CONFIG is set to empty string")
	}

	envAgentID, hasEnvAgentID := envLookup("ASTRORDER_CALLER_AGENT_ID")
	if hasEnvAgentID && envAgentID == "" {
		return nil, errors.New("ASTRORDER_CALLER_AGENT_ID is set to empty string")
	}
	hasEnvAgentID = hasEnvAgentID && envAgentID != ""

	envConnID, hasEnvConnID := envLookup("ASTRORDER_CALLER_CONNECTION_ID")
	if hasEnvConnID && envConnID == "" {
		return nil, errors.New("ASTRORDER_CALLER_CONNECTION_ID is set to empty string")
	}
	hasEnvConnID = hasEnvConnID && envConnID != ""

	envProfName, hasEnvProfName := envLookup("ASTRORDER_CALLER_PROFILE_NAME")
	if hasEnvProfName && envProfName == "" {
		return nil, errors.New("ASTRORDER_CALLER_PROFILE_NAME is set to empty string")
	}
	hasEnvProfName = hasEnvProfName && envProfName != ""

	envRuntime, hasEnvRuntime := envLookup("ASTRORDER_CALLER_RUNTIME")
	if hasEnvRuntime && envRuntime == "" {
		return nil, errors.New("ASTRORDER_CALLER_RUNTIME is set to empty string")
	}
	hasEnvRuntime = hasEnvRuntime && envRuntime != ""

	envSessionID, hasEnvSessionID := envLookup("ASTRORDER_CALLER_SESSION_ID")
	if hasEnvSessionID && envSessionID == "" {
		return nil, errors.New("ASTRORDER_CALLER_SESSION_ID is set to empty string")
	}
	hasEnvSessionID = hasEnvSessionID && envSessionID != ""

	hermesID, hasHermes := envLookup("HERMES_SESSION_ID")
	hasHermes = hasHermes && hermesID != ""
	codexID, hasCodex := envLookup("CODEX_THREAD_ID")
	hasCodex = hasCodex && codexID != ""
	grokID, hasGrok := envLookup("GROK_SESSION_ID")
	hasGrok = hasGrok && grokID != ""

	runtimeCount := 0
	if hasHermes {
		runtimeCount++
	}
	if hasCodex {
		runtimeCount++
	}
	if hasGrok {
		runtimeCount++
	}

	var fileCfg *callerConfigFile
	if hasConfig && configPath != "" {
		var err error
		fileCfg, err = readCallerConfig(configPath, config.ReadFile)
		if err != nil {
			return nil, err
		}
	} else if runtimeCount > 1 && !hasEnvRuntime {
		return nil, errors.New("multiple runtime session variables detected; ASTRORDER_CALLER_RUNTIME or explicit ASTRORDER_CALLER_CONFIG must be set")
	} else if runtimeCount <= 1 && (hasHermes || (hasEnvRuntime && envRuntime == "hermes")) {
		var err error
		fileCfg, err = findHermesCallerConfig(envLookup, config.ReadFile)
		if err != nil {
			return nil, err
		}
	}

	if fileCfg != nil {
		if hasEnvAgentID && envAgentID != fileCfg.AgentID {
			return nil, fmt.Errorf("conflict between environment ASTRORDER_CALLER_AGENT_ID %q and caller config agent_id %q", envAgentID, fileCfg.AgentID)
		}
		if hasEnvConnID && envConnID != fileCfg.ConnectionID {
			return nil, fmt.Errorf("conflict between environment ASTRORDER_CALLER_CONNECTION_ID %q and caller config connection_id %q", envConnID, fileCfg.ConnectionID)
		}
		if hasEnvProfName && envProfName != fileCfg.ProfileName {
			return nil, fmt.Errorf("conflict between environment ASTRORDER_CALLER_PROFILE_NAME %q and caller config profile_name %q", envProfName, fileCfg.ProfileName)
		}
		if hasEnvRuntime && envRuntime != fileCfg.Runtime {
			return nil, fmt.Errorf("conflict between environment ASTRORDER_CALLER_RUNTIME %q and caller config runtime %q", envRuntime, fileCfg.Runtime)
		}
	}

	agentID := envAgentID
	if agentID == "" && fileCfg != nil {
		agentID = fileCfg.AgentID
	}
	if agentID == "" {
		return nil, errors.New("missing required environment variable ASTRORDER_CALLER_AGENT_ID (or set in ASTRORDER_CALLER_CONFIG)")
	}
	if containsWhitespace(agentID) {
		return nil, fmt.Errorf("caller agent_id contains illegal whitespace: %q", agentID)
	}

	connID := envConnID
	if connID == "" && fileCfg != nil {
		connID = fileCfg.ConnectionID
	}
	if connID == "" {
		return nil, errors.New("missing required environment variable ASTRORDER_CALLER_CONNECTION_ID (or set in ASTRORDER_CALLER_CONFIG)")
	}
	if containsWhitespace(connID) {
		return nil, fmt.Errorf("caller connection_id contains illegal whitespace: %q", connID)
	}

	profName := envProfName
	if profName == "" && fileCfg != nil {
		profName = fileCfg.ProfileName
	}
	if profName == "" {
		return nil, errors.New("missing required environment variable ASTRORDER_CALLER_PROFILE_NAME (or set in ASTRORDER_CALLER_CONFIG)")
	}
	if containsWhitespace(profName) {
		return nil, fmt.Errorf("caller profile_name contains illegal whitespace: %q", profName)
	}

	explicitRuntime := envRuntime
	if explicitRuntime == "" && fileCfg != nil {
		explicitRuntime = fileCfg.Runtime
	}
	hasExplicitRuntime := explicitRuntime != ""

	if runtimeCount > 1 && !hasExplicitRuntime {
		return nil, errors.New("multiple runtime session variables detected; ASTRORDER_CALLER_RUNTIME must be set explicitly")
	}

	var runtime string
	var autoSessionID string

	if hasExplicitRuntime {
		if containsWhitespace(explicitRuntime) {
			return nil, fmt.Errorf("caller runtime contains illegal whitespace: %q", explicitRuntime)
		}
		runtime = explicitRuntime
		switch runtime {
		case "hermes":
			if hasHermes {
				autoSessionID = hermesID
			}
		case "codex":
			if hasCodex {
				autoSessionID = codexID
			}
		case "grok":
			if hasGrok {
				autoSessionID = grokID
			}
		default:
			// Unsupported runtime: requires explicit session ID
		}
	} else {
		if hasHermes && !hasCodex && !hasGrok {
			runtime = "hermes"
			autoSessionID = hermesID
		} else if hasCodex && !hasHermes && !hasGrok {
			runtime = "codex"
			autoSessionID = codexID
		} else if hasGrok && !hasHermes && !hasCodex {
			return nil, errors.New("grok runtime is experimental and requires explicit ASTRORDER_CALLER_RUNTIME or caller config")
		} else {
			return nil, errors.New("cannot determine caller runtime; set ASTRORDER_CALLER_RUNTIME or ASTRORDER_CALLER_CONFIG")
		}
	}

	if autoSessionID != "" && containsWhitespace(autoSessionID) {
		return nil, fmt.Errorf("runtime %s session ID contains illegal whitespace: %q", runtime, autoSessionID)
	}

	var sessionID string
	if hasEnvSessionID {
		if containsWhitespace(envSessionID) {
			return nil, fmt.Errorf("ASTRORDER_CALLER_SESSION_ID contains illegal whitespace: %q", envSessionID)
		}
		if autoSessionID != "" && envSessionID != autoSessionID {
			return nil, fmt.Errorf("explicit session ID %q does not match runtime %s session ID %q", envSessionID, runtime, autoSessionID)
		}
		sessionID = envSessionID
	} else {
		if runtime != "hermes" && runtime != "codex" && runtime != "grok" {
			return nil, fmt.Errorf("unsupported runtime %q requires explicit ASTRORDER_CALLER_SESSION_ID", runtime)
		}
		if autoSessionID == "" {
			return nil, fmt.Errorf("missing caller session ID for runtime %q", runtime)
		}
		sessionID = autoSessionID
	}

	return &Caller{
		AgentID:      agentID,
		ConnectionID: connID,
		ProfileName:  profName,
		Runtime:      runtime,
		SessionID:    sessionID,
	}, nil
}

func requiresCaller(capability string) bool {
	if strings.HasPrefix(capability, "sessions.") || capability == "machines.dispatch" {
		return true
	}
	switch capability {
	case "swarm.telemetry.report",
		"swarm.lock.acquire",
		"swarm.lock.release",
		"swarm.sos.escalate",
		"plugins.open",
		"plugins.close":
		return true
	}
	if strings.HasPrefix(capability, "blackboard.") {
		if capability == "blackboard.components.list" || capability == "blackboard.component.schema" {
			return false
		}
		return true
	}
	if strings.HasPrefix(capability, "browser.") {
		return true
	}
	return false
}

// applyCallerScope binds session-scoped fields to the resolved caller. Omitted
// fields are filled so the server never records an "anonymous_worker"; explicit
// null, empty or non-string values are rejected instead of silently replaced.
// Lock owners and SOS workers must be the caller; plugin targets may be explicit
// because the server already routes an explicit session_key.
func applyCallerScope(capability string, payload map[string]any, caller *Caller) error {
	callerKey := caller.AgentID + "::" + caller.SessionID
	switch capability {
	case "swarm.lock.acquire", "swarm.lock.release":
		return bindCallerField(payload, "owner_key", callerKey, false)
	case "swarm.sos.escalate":
		return bindCallerField(payload, "key", callerKey, false)
	case "plugins.open", "plugins.close":
		// The server already routes an explicit target given as agent_id +
		// session_id. Do not shadow it with the caller's session_key.
		if raw, present := payload["session_id"]; present && raw != nil {
			if _, hasKey := payload["session_key"]; !hasKey {
				return nil
			}
		}
		return bindCallerField(payload, "session_key", callerKey, true)
	}
	return nil
}

func bindCallerField(payload map[string]any, field, callerKey string, allowExplicitTarget bool) error {
	raw, present := payload[field]
	if !present {
		payload[field] = callerKey
		return nil
	}
	if raw == nil {
		return fmt.Errorf("%s must not be null", field)
	}
	value, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", field)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if !allowExplicitTarget && value != callerKey {
		return fmt.Errorf("%s %q does not match configured caller %q", field, value, callerKey)
	}
	return nil
}

func injectCaller(payload map[string]any, caller *Caller) error {
	if rawCaller, ok := payload["caller"]; ok {
		if rawCaller == nil {
			return errors.New("caller in payload must not be null")
		}
		callerMap, ok := rawCaller.(map[string]any)
		if !ok {
			return errors.New("caller in payload must be an object")
		}
		checkField := func(key, expected string) error {
			if val, ok := callerMap[key]; ok {
				if s, ok := val.(string); !ok || s != expected {
					return fmt.Errorf("payload caller.%s %v does not match configured caller %q", key, val, expected)
				}
			}
			return nil
		}
		if err := checkField("agent_id", caller.AgentID); err != nil {
			return err
		}
		if err := checkField("connection_id", caller.ConnectionID); err != nil {
			return err
		}
		if err := checkField("profile_name", caller.ProfileName); err != nil {
			return err
		}
		if err := checkField("runtime", caller.Runtime); err != nil {
			return err
		}
		if err := checkField("session_id", caller.SessionID); err != nil {
			return err
		}
	}
	payload["caller"] = map[string]any{
		"agent_id":      caller.AgentID,
		"connection_id": caller.ConnectionID,
		"profile_name":  caller.ProfileName,
		"runtime":       caller.Runtime,
		"session_id":    caller.SessionID,
	}
	return nil
}
