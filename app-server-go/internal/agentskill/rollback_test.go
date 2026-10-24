package agentskill

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRollsBackAfterWrites(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, verifyFailure := range []bool{false, true} {
			t.Run(map[bool]string{false: "fresh", true: "existing"}[existing]+map[bool]string{false: "_write", true: "_verify"}[verifyFailure], func(t *testing.T) {
				home := t.TempDir()
				opts := InstallOptions{Runtime: "hermes", Home: home, CLI: getTestCLI(t), AgentID: "hermes", ConnectionID: "local", ProfileName: "default"}
				paths := []string{filepath.Join(home, CallerFile), filepath.Join(home, "skills", "astrorder", "scripts", targetBinaryName(opts.CLI)), filepath.Join(home, "skills", "astrorder", SkillMDFile), filepath.Join(home, "skills", "astrorder", ManifestFile)}
				before := map[string][]byte{}
				if existing {
					if _, err := Install(opts); err != nil {
						t.Fatal(err)
					}
					for _, p := range paths {
						b, err := os.ReadFile(p)
						if err != nil {
							t.Fatal(err)
						}
						before[p] = b
					}
				}
				writes := 0
				injected := errors.New("injected manifest write failure")
				_, err := install(opts, func(p string, b []byte, perm os.FileMode) error {
					writes++
					if writes == 4 && !verifyFailure {
						return injected
					}
					if err := atomicWriteFile(p, b, perm); err != nil {
						return err
					}
					if writes == 4 && verifyFailure {
						return os.WriteFile(paths[2], []byte("corrupted readback"), 0644)
					}
					return nil
				})
				if err == nil || writes != 4 {
					t.Fatalf("expected failure after four writes, writes=%d err=%v", writes, err)
				}
				if !verifyFailure && !errors.Is(err, injected) {
					t.Fatalf("lost original error: %v", err)
				}
				for _, p := range paths {
					got, readErr := os.ReadFile(p)
					if existing {
						if readErr != nil || !bytes.Equal(got, before[p]) {
							t.Fatalf("rollback changed %s: %v", p, readErr)
						}
					} else if !errors.Is(readErr, os.ErrNotExist) {
						t.Fatalf("new target remains %s: %v", p, readErr)
					}
				}
			})
		}
	}
}
