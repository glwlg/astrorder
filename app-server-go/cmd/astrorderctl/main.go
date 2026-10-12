package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"astrorder.dev/app-server/internal/acceptance"
	"astrorder.dev/app-server/internal/parity"
	"astrorder.dev/app-server/internal/productregistry"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func runIsolatedVerify(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	isolated := flags.Bool("isolated", false, "run only the bounded private App acceptance slice")
	root := flags.String("root", "", "absolute repository/evidence root")
	appExecutable := flags.String("app-executable", "", "absolute freshly built App candidate")
	outputDir := flags.String("output-dir", "", "absolute fresh output directory below root")
	candidateRevision := flags.String("candidate-revision", "", "expected full candidate Git revision")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, fmt.Errorf("unexpected verify positional arguments"))
	}
	if !*isolated || *root == "" || *appExecutable == "" || *outputDir == "" || *candidateRevision == "" {
		return cliFailure(output, fmt.Errorf("verify requires --isolated, --root, --app-executable, --output-dir and --candidate-revision"))
	}
	controlExecutable, err := os.Executable()
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve astrorderctl executable: %w", err))
	}
	controlExecutable, err = filepath.EvalSymlinks(controlExecutable)
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve astrorderctl executable: %w", err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := acceptance.Run(ctx, acceptance.Options{
		Root: *root, AppExecutable: *appExecutable, OutputDir: *outputDir,
		CandidateRevision: *candidateRevision, ControlExecutable: controlExecutable,
	})
	if err != nil {
		_ = json.NewEncoder(output).Encode(map[string]any{
			"ok": false, "complete_migration": false, "scope": "go-app-foundation-isolated", "error": err.Error(), "partial": result,
		})
		return 1
	}
	return cliSuccess(output, result)
}

func run(args []string, output io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "verify":
			return runIsolatedVerify(args[1:], output)
		case "provision":
			return runProvision(args[1:], output)
		case "app":
			return runApp(args[1:], output)
		case "daemon":
			return runDaemon(args[1:], output)
		case "service":
			return runService(args[1:], output)
		case "login":
			return runLogin(args[1:], output)
		case "sync":
			return runSync(args[1:], output)
		}
	}
	fail := func(message string) int {
		_ = json.NewEncoder(output).Encode(map[string]any{"ok": false, "error": message})
		return 1
	}
	if len(args) < 2 || args[0] != "migration" || (args[1] != "inventory" && args[1] != "verify") {
		return fail("usage: astrorderctl migration inventory|verify --root PATH [--baseline PATH] [--matrix PATH] [--structural]")
	}
	flags := flag.NewFlagSet("migration "+args[1], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "repository or installed migration asset root")
	baselinePath := flags.String("baseline", "docs/reports/full-go-baseline.json", "frozen baseline path")
	matrixPath := flags.String("matrix", "docs/reports/full-go-migration-matrix.json", "migration manifest path")
	structural := flags.Bool("structural", false, "validate mapping structure without final completion requirements")
	if err := flags.Parse(args[2:]); err != nil {
		return fail(err.Error())
	}
	if flags.NArg() != 0 {
		return fail("unexpected positional arguments")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return fail(err.Error())
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(absolute, filepath.FromSlash(path))
	}
	baseline, err := parity.LoadBaseline(resolve(*baselinePath))
	if err != nil {
		return fail(fmt.Sprintf("load frozen inventory: %v", err))
	}
	entries := baseline.Entries
	if args[1] == "inventory" {
		if err := json.NewEncoder(output).Encode(map[string]any{"ok": true, "source": "frozen_baseline", "baseline_revision": baseline.Revision, "baseline_hash": baseline.Hash, "inventory": baseline.Document, "entries": entries}); err != nil {
			return 1
		}
		return 0
	}
	manifest, err := parity.Load(resolve(*matrixPath))
	if err != nil {
		return fail(fmt.Sprintf("load migration matrix: %v", err))
	}
	if manifest.FrozenHash != baseline.Hash || manifest.BaselineRevision != baseline.Revision {
		return fail("migration matrix does not match frozen inventory revision/hash")
	}
	manifest.BaselineHash = parity.EntryHash(entries)
	result := parity.Verify(absolute, manifest, entries, false)
	if !*structural {
		// Providers are compiled from executable definitions. Domains still
		// being migrated stay uncovered; documentary DONE never creates handlers.
		result = parity.VerifyRuntime(absolute, manifest, entries, productregistry.Snapshot())
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return 1
	}
	if !result.OK {
		return 1
	}
	return 0
}
