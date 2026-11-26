package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/lifecycle"
)

func runSync(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	devRoot := flags.String("dev-root", "", "repository workspace root")
	installedRoot := flags.String("installed-root", "", "installed server root")
	verifyOnly := flags.Bool("verify-only", false, "verify installed frontend/dist without copying")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, fmt.Errorf("unexpected sync arguments: %v", flags.Args()))
	}
	if *verifyOnly {
		if *installedRoot == "" {
			return cliFailure(output, errors.New("sync --verify-only requires --installed-root"))
		}
		absInstalled, err := filepath.Abs(*installedRoot)
		if err != nil {
			return cliFailure(output, err)
		}
		distDest := filepath.Join(absInstalled, "frontend", "dist")
		if err := lifecycle.ValidateBrowserDist(distDest); err != nil {
			return cliFailure(output, fmt.Errorf("installed frontend/dist failed integrity check: %w", err))
		}
		return cliSuccess(output, map[string]any{
			"ok":             true,
			"verified":       "frontend/dist",
			"installed_root": absInstalled,
		})
	}
	if *devRoot == "" || *installedRoot == "" {
		return cliFailure(output, errors.New("sync requires --dev-root and --installed-root"))
	}
	absDev, err := filepath.Abs(*devRoot)
	if err != nil {
		return cliFailure(output, err)
	}
	absInstalled, err := filepath.Abs(*installedRoot)
	if err != nil {
		return cliFailure(output, err)
	}

	distSrc := filepath.Join(absDev, "frontend", "dist")
	if info, err := os.Stat(distSrc); err != nil || !info.IsDir() {
		return cliFailure(output, fmt.Errorf("dev frontend/dist not found at %s: build frontend first", distSrc))
	}
	if err := lifecycle.ValidateBrowserDist(distSrc); err != nil {
		return cliFailure(output, fmt.Errorf("dev frontend/dist failed integrity check at %s: %w", distSrc, err))
	}

	distDest := filepath.Join(absInstalled, "frontend", "dist")
	if pathsOverlap(distSrc, distDest) {
		return cliFailure(output, errors.New("sync source and destination frontend/dist must not overlap"))
	}
	if err := copyDirAtomic(distSrc, distDest, lifecycle.ValidateBrowserDist); err != nil {
		return cliFailure(output, fmt.Errorf("sync frontend dist: %w", err))
	}

	return cliSuccess(output, map[string]any{
		"ok":             true,
		"synced":         "frontend/dist",
		"dev_root":       absDev,
		"installed_root": absInstalled,
	})
}

func copyDirAtomic(src, dest string, validate func(string) error) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmpDest, err := os.MkdirTemp(parent, ".sync-tmp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDest)

	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed in frontend/dist: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmpDest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file not allowed in frontend/dist: %s", path)
		}
		return copyFile(path, target)
	})
	if err != nil {
		return err
	}
	// Re-validate the staged copy before swapping so a concurrent frontend
	// rebuild cannot leave the installed frontend/dist half-written.
	if validate != nil {
		if err := validate(tmpDest); err != nil {
			return fmt.Errorf("staged frontend/dist failed integrity check before swap: %w", err)
		}
	}

	backupDest := dest + ".old"
	_ = os.RemoveAll(backupDest)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, backupDest); err != nil {
			// Windows may fail rename if files are locked
			return err
		}
	}
	if err := os.Rename(tmpDest, dest); err != nil {
		if _, statErr := os.Stat(backupDest); statErr == nil {
			_ = os.Rename(backupDest, dest)
		}
		return err
	}
	_ = os.RemoveAll(backupDest)
	return nil
}

func pathsOverlap(a, b string) bool {
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if safeLower(ca) == safeLower(cb) {
		return true
	}
	for _, pair := range [][2]string{{ca, cb}, {cb, ca}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func safeLower(s string) string {
	return strings.ToLower(filepath.Clean(s))
}
