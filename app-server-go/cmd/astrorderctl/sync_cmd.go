package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runSync(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	devRoot := flags.String("dev-root", "", "repository workspace root")
	installedRoot := flags.String("installed-root", "", "installed server root")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
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

	distDest := filepath.Join(absInstalled, "frontend", "dist")
	if err := copyDirAtomic(distSrc, distDest); err != nil {
		return cliFailure(output, fmt.Errorf("sync frontend dist: %w", err))
	}

	return cliSuccess(output, map[string]any{
		"ok":             true,
		"synced":         "frontend/dist",
		"dev_root":       absDev,
		"installed_root": absInstalled,
	})
}

func copyDirAtomic(src, dest string) error {
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
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmpDest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		return err
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
