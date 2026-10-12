package agentremote

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
)

// RunCLI implements the legacy Python astrorder.agents.cli command router.
func RunCLI(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer, lookup EnvLookup, readFile FileReader) int {
	cfg, err := ResolveConfig(lookup, readFile)
	if err != nil {
		fmt.Fprintf(errOut, "%v\n", err)
		return 1
	}
	client, err := NewClient(cfg)
	if err != nil {
		fmt.Fprintf(errOut, "create client: %v\n", err)
		return 1
	}

	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: astrorder-agent catalog|invoke|sessions|agents|machines|projects")
		return 2
	}

	cmd := args[0]
	rest := args[1:]

	var capability string
	var payload map[string]any

	switch cmd {
	case "catalog":
		if len(rest) > 0 {
			fmt.Fprintf(errOut, "catalog takes no arguments, got: %v\n", rest)
			return 2
		}
		capability = "catalog.list"
		payload = map[string]any{}

	case "invoke":
		flags := flag.NewFlagSet("invoke", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		inputStr := flags.String("input", "{}", "JSON object")
		if err := flags.Parse(rest); err != nil {
			fmt.Fprintf(errOut, "%v\n", err)
			return 2
		}
		if flags.NArg() < 1 {
			fmt.Fprintln(errOut, "capability argument required")
			return 2
		}
		capability = flags.Arg(0)
		dec := json.NewDecoder(io.LimitReader(flagReader(*inputStr), MaxBusinessBytes))
		dec.UseNumber()
		if err := dec.Decode(&payload); err != nil || payload == nil {
			fmt.Fprintln(errOut, "--input must be a JSON object")
			return 1
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			fmt.Fprintln(errOut, "--input must be a JSON object")
			return 1
		}

	case "sessions":
		if len(rest) == 0 {
			fmt.Fprintln(errOut, "sessions subcommand required: list|search|read")
			return 2
		}
		sub := rest[0]
		subArgs := rest[1:]
		switch sub {
		case "list":
			if len(subArgs) > 0 {
				fmt.Fprintf(errOut, "sessions list takes no arguments, got: %v\n", subArgs)
				return 2
			}
			capability = "sessions.list"
			payload = map[string]any{}
		case "search":
			if len(subArgs) != 1 {
				fmt.Fprintln(errOut, "search query q required")
				return 2
			}
			capability = "sessions.search"
			payload = map[string]any{"q": subArgs[0]}
		case "read":
			flags := flag.NewFlagSet("sessions read", flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			key := flags.String("key", "", "session key")
			agentID := flags.String("agent-id", "", "agent id")
			sessionID := flags.String("session-id", "", "session id")
			limit := flags.Int("limit", 80, "limit")
			if err := flags.Parse(subArgs); err != nil {
				fmt.Fprintf(errOut, "%v\n", err)
				return 2
			}
			if flags.NArg() != 0 {
				fmt.Fprintf(errOut, "unexpected positional arguments: %v\n", flags.Args())
				return 2
			}
			capability = "sessions.read"
			p := map[string]any{"limit": *limit}
			if *key != "" {
				p["key"] = *key
			}
			if *agentID != "" {
				p["agent_id"] = *agentID
			}
			if *sessionID != "" {
				p["session_id"] = *sessionID
			}
			payload = p
		default:
			fmt.Fprintf(errOut, "unknown sessions subcommand: %s\n", sub)
			return 2
		}

	case "agents":
		if len(rest) > 0 {
			fmt.Fprintf(errOut, "agents takes no arguments, got: %v\n", rest)
			return 2
		}
		capability = "agents.list"
		payload = map[string]any{}
	case "machines":
		if len(rest) > 0 {
			fmt.Fprintf(errOut, "machines takes no arguments, got: %v\n", rest)
			return 2
		}
		capability = "machines.list"
		payload = map[string]any{}
	case "projects":
		if len(rest) > 0 {
			fmt.Fprintf(errOut, "projects takes no arguments, got: %v\n", rest)
			return 2
		}
		capability = "projects.list"
		payload = map[string]any{}
	default:
		fmt.Fprintf(errOut, "unknown command: %s\n", cmd)
		return 2
	}

	result, err := client.Invoke(ctx, capability, payload)
	if err != nil {
		fmt.Fprintf(errOut, "%v\n", err)
		return 1
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(errOut, "encode output: %v\n", err)
		return 1
	}
	return 0
}

type stringReader string

func (s stringReader) Read(p []byte) (n int, err error) {
	if len(s) == 0 {
		return 0, io.EOF
	}
	n = copy(p, s)
	return n, io.EOF
}

func flagReader(s string) io.Reader {
	return stringReader(s)
}

func parsePortSafe(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func _unused() {
	_ = errors.New("")
}
