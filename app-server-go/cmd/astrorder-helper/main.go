// astrorder-helper executes versioned filesystem and Git requests over stdio.
// It is intended for local, WSL, and SSH targets and does not host Agent
// processes or App HTTP routes.
//
// It also doubles as the native-activity bridge, so hooks need no Python:
//
//	astrorder-helper observe --spool DIR   Codex hook: spool one event from stdin
//	astrorder-helper tail    --spool DIR   stream spooled events as JSON lines
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"astrorder.dev/app-server/internal/observe"
	"astrorder.dev/app-server/internal/targetfs"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "observe":
			runObserve(os.Args[2:])
			return
		case "tail":
			runTail(os.Args[2:])
			return
		case "install-hooks":
			runInstallHooks(os.Args[2:])
			return
		}
	}
	stdio := flag.Bool("stdio", false, "serve one request over stdin/stdout")
	protocol := flag.Int("protocol", 0, "required helper protocol version")
	flag.Parse()
	if !*stdio {
		fmt.Fprintln(os.Stderr, "astrorder-helper: --stdio is required")
		os.Exit(2)
	}
	if *protocol != targetfs.ProtocolVersion {
		fmt.Fprintf(os.Stderr, "astrorder-helper: protocol %d is unsupported; require %d\n", *protocol, targetfs.ProtocolVersion)
		os.Exit(2)
	}
	if err := targetfs.ServeHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "astrorder-helper: %v\n", err)
		os.Exit(1)
	}
}

// runObserve is a Codex hook. It prints nothing and always exits 0: a failed
// bridge makes no decision, so Codex falls back to its native approval flow.
func runObserve(args []string) {
	set := flag.NewFlagSet("observe", flag.ContinueOnError)
	spool := set.String("spool", "", "spool directory")
	set.SetOutput(os.Stderr)
	if err := set.Parse(args); err != nil || *spool == "" {
		return
	}
	_ = observe.Run(*spool, os.Stdin, time.Now())
}

// runInstallHooks points the Astrorder Codex hooks at this helper and has Codex
// trust them. It always prints one JSON result so a remote caller can read the
// outcome over SSH; only a usage error exits non-zero.
func runInstallHooks(args []string) {
	set := flag.NewFlagSet("install-hooks", flag.ContinueOnError)
	home := set.String("codex-home", "", "Codex home (default $CODEX_HOME or ~/.codex)")
	spool := set.String("spool", "", "event spool directory")
	codex := set.String("codex", "", "Codex executable used to record trust")
	noTrust := set.Bool("no-trust", false, "rewrite hooks but do not record trust")
	set.SetOutput(os.Stderr)
	if err := set.Parse(args); err != nil {
		os.Exit(2)
	}
	result := observe.InstallHooks(context.Background(), observe.InstallOptions{
		CodexHome: *home, Spool: *spool, Codex: *codex, Trust: !*noTrust,
	})
	_ = json.NewEncoder(os.Stdout).Encode(result)
}

// runTail streams new spool events to stdout, one JSON object per line. The
// session daemon runs it over SSH/WSL so remote Agents use the same event path
// as local ones. It never replays history: it starts at the end of the spool.
func runTail(args []string) {
	set := flag.NewFlagSet("tail", flag.ContinueOnError)
	spool := set.String("spool", "", "spool directory")
	interval := set.Duration("interval", 500*time.Millisecond, "poll interval")
	set.SetOutput(os.Stderr)
	if err := set.Parse(args); err != nil || *spool == "" {
		os.Exit(2)
	}
	if err := observe.Tail(context.Background(), *spool, *interval, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "astrorder-helper: %v\n", err)
		os.Exit(1)
	}
}
