// Package agentcli implements the public astrorder-agent command as a thin,
// single-attempt HTTP client. Run is reusable by a future astrorderctl adapter.
package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	programName           = "astrorder-agent"
	defaultBaseURL        = "http://127.0.0.1:30001"
	invokePath            = "/api/v1/agent/invoke"
	defaultRequestTimeout = 60 * time.Second
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
	name       string
	help       string
	children   []*commandDefinition
	options    []optionDefinition
	capability func(map[string]any) (string, map[string]any, error)
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
			capability: func(map[string]any) (string, map[string]any, error) {
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
			{name: "input", flags: []string{"--input"}, metavar: "INPUT", kind: kindJSON, hasDefault: true, defaultValue: "{}", help: "JSON object"},
		},
		capability: func(values map[string]any) (string, map[string]any, error) {
			payload, err := decodeObject(values["input"].(string))
			if err != nil {
				if err.Error() == "--input must be a JSON object" {
					return "", nil, err
				}
				return "", nil, fmt.Errorf("invalid --input JSON: %w", err)
			}
			return values["capability"].(string), payload, nil
		},
	}
	sessionsList := simple("list", "", "sessions.list")
	sessionsSearch := &commandDefinition{
		name:    "search",
		options: []optionDefinition{{name: "q", metavar: "q", kind: kindString, positional: true, required: true}},
		capability: func(values map[string]any) (string, map[string]any, error) {
			return "sessions.search", map[string]any{"q": values["q"]}, nil
		},
	}
	sessionsRead := &commandDefinition{
		name: "read",
		options: []optionDefinition{
			{name: "key", flags: []string{"--key"}, metavar: "KEY", kind: kindString},
			{name: "agent_id", flags: []string{"--agent-id"}, metavar: "AGENT_ID", kind: kindString},
			{name: "session_id", flags: []string{"--session-id"}, metavar: "SESSION_ID", kind: kindString},
			{name: "limit", flags: []string{"--limit"}, metavar: "LIMIT", kind: kindInteger, hasDefault: true, defaultValue: 80},
		},
		capability: func(values map[string]any) (string, map[string]any, error) {
			payload := map[string]any{"limit": values["limit"]}
			for _, name := range []string{"key", "agent_id", "session_id"} {
				if value, ok := values[name]; ok && value != "" {
					payload[name] = value
				}
			}
			return "sessions.read", payload, nil
		},
	}
	sessions := &commandDefinition{name: "sessions", children: []*commandDefinition{sessionsList, sessionsSearch, sessionsRead}}
	return &commandDefinition{
		children: []*commandDefinition{
			catalog,
			invoke,
			sessions,
			simple("agents", "", "agents.list"),
			simple("machines", "", "machines.list"),
			simple("projects", "", "projects.list"),
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
	capability, payload, err := parsed.command.capability(parsed.values)
	if err != nil {
		_, _ = fmt.Fprintln(errWriter, err)
		return 1
	}
	result, err := invoke(ctx, capability, payload, envLookup, config)
	if err != nil {
		_, _ = fmt.Fprintln(errWriter, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
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
	token, err := resolveToken(envLookup, config.ReadFile)
	if err != nil {
		return nil, err
	}
	baseURL := defaultBaseURL
	if value, ok := envLookup("ASTRORDER_AGENT_URL"); ok {
		baseURL = value
	}
	body, err := marshalRequest(map[string]any{"capability": capability, "input": payload})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	timeout := config.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
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

func resolveToken(envLookup EnvLookup, readFile func(string) ([]byte, error)) (string, error) {
	for _, key := range []string{"ASTRORDER_AGENT_TOKEN", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET"} {
		if value, ok := envLookup(key); ok && value != "" {
			return value, nil
		}
	}
	path := ".token"
	if value, ok := envLookup("ASTRORDER_TOKEN_FILE"); ok && value != "" {
		path = value
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	if raw, err := readFile(path); err == nil {
		return strings.TrimSpace(string(raw)), nil
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
