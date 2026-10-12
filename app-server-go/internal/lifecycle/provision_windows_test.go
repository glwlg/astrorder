//go:build windows

package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProvisionRejectsDirectoryJunctionEscapesBeforeSideEffects(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))

	t.Run("executable escapes installation root", func(t *testing.T) {
		junctionRoot := filepath.Join(base, "junction-install")
		mustMkdir(t, filepath.Join(junctionRoot, "frontend", "dist"))
		mustWrite(t, filepath.Join(junctionRoot, "frontend", "dist", "index.html"), []byte("fixture"))
		outsideBin := filepath.Join(base, "outside-bin")
		mustMkdir(t, outsideBin)
		outsideExecutable := filepath.Join(outsideBin, "astrorder-app.exe")
		mustWrite(t, outsideExecutable, []byte("outside fixture executable"))
		createDirectoryJunction(t, filepath.Join(junctionRoot, "bin"), outsideBin)
		stateDir := filepath.Join(base, "executable-state")

		_, err := Provision(Options{
			RootDir:       junctionRoot,
			StateDir:      stateDir,
			DataDir:       filepath.Join(stateDir, "data"),
			AppExecutable: filepath.Join(junctionRoot, "bin", "astrorder-app.exe"),
		})
		if err == nil {
			t.Fatal("App executable junction outside installation root was accepted")
		}
		if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
			t.Fatalf("rejected executable escape touched state directory: %v", statErr)
		}
	})

	t.Run("static assets escape installation root", func(t *testing.T) {
		junctionRoot := filepath.Join(base, "static-junction-install")
		mustMkdir(t, junctionRoot)
		junctionExecutable := filepath.Join(junctionRoot, "astrorder-app.exe")
		mustWrite(t, junctionExecutable, []byte("fixture executable"))
		outsideFrontend := filepath.Join(base, "outside-frontend")
		mustMkdir(t, filepath.Join(outsideFrontend, "dist"))
		mustWrite(t, filepath.Join(outsideFrontend, "dist", "index.html"), []byte("outside fixture"))
		createDirectoryJunction(t, filepath.Join(junctionRoot, "frontend"), outsideFrontend)
		stateDir := filepath.Join(base, "static-state")

		_, err := Provision(Options{
			RootDir:       junctionRoot,
			StateDir:      stateDir,
			DataDir:       filepath.Join(stateDir, "data"),
			AppExecutable: junctionExecutable,
		})
		if err == nil {
			t.Fatal("static directory junction outside installation root was accepted")
		}
		if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
			t.Fatalf("rejected static escape touched state directory: %v", statErr)
		}
	})

	t.Run("state directory escapes declared state path", func(t *testing.T) {
		outsideState := filepath.Join(base, "outside-state")
		mustMkdir(t, outsideState)
		stateDir := filepath.Join(base, "state-junction")
		createDirectoryJunction(t, stateDir, outsideState)
		beforeACL := pathSDDL(t, outsideState)

		_, err := Provision(Options{
			RootDir:       root,
			StateDir:      stateDir,
			DataDir:       filepath.Join(stateDir, "data"),
			AppExecutable: appExecutable,
		})
		if err == nil {
			t.Fatal("state directory junction was accepted")
		}
		if afterACL := pathSDDL(t, outsideState); afterACL != beforeACL {
			t.Fatal("rejected state escape altered outside target ACL")
		}
		for _, target := range []string{
			filepath.Join(outsideState, "data"),
			filepath.Join(outsideState, configName),
			filepath.Join(outsideState, credentialsName),
		} {
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Fatalf("rejected state escape wrote target %s: %v", target, statErr)
			}
		}
	})

	t.Run("data escapes state root", func(t *testing.T) {
		stateDir := filepath.Join(base, "data-state")
		mustMkdir(t, stateDir)
		outsideData := filepath.Join(base, "outside-data")
		mustMkdir(t, outsideData)
		createDirectoryJunction(t, filepath.Join(stateDir, "data"), outsideData)
		beforeACL := pathSDDL(t, outsideData)

		_, err := Provision(Options{
			RootDir:       root,
			StateDir:      stateDir,
			DataDir:       filepath.Join(stateDir, "data"),
			AppExecutable: appExecutable,
		})
		if err == nil {
			t.Fatal("data directory junction outside state root was accepted")
		}
		if afterACL := pathSDDL(t, outsideData); afterACL != beforeACL {
			t.Fatal("rejected data escape altered outside target ACL")
		}
		for _, target := range []string{
			filepath.Join(outsideData, "attachments"),
			filepath.Join(stateDir, configName),
			filepath.Join(stateDir, credentialsName),
		} {
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Fatalf("rejected data escape wrote target %s: %v", target, statErr)
			}
		}
	})

	t.Run("data attachments escape state root", func(t *testing.T) {
		stateDir := filepath.Join(base, "attachments-state")
		dataDir := filepath.Join(stateDir, "data")
		mustMkdir(t, dataDir)
		outsideAttachments := filepath.Join(base, "outside-attachments")
		mustMkdir(t, outsideAttachments)
		createDirectoryJunction(t, filepath.Join(dataDir, "attachments"), outsideAttachments)
		beforeACL := pathSDDL(t, outsideAttachments)

		_, err := Provision(Options{
			RootDir:       root,
			StateDir:      stateDir,
			DataDir:       dataDir,
			AppExecutable: appExecutable,
		})
		if err == nil {
			t.Fatal("attachments junction outside state root was accepted")
		}
		if afterACL := pathSDDL(t, outsideAttachments); afterACL != beforeACL {
			t.Fatal("rejected attachments escape altered outside target ACL")
		}
		for _, target := range []string{
			filepath.Join(stateDir, configName),
			filepath.Join(stateDir, credentialsName),
		} {
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Fatalf("rejected attachments escape wrote target %s: %v", target, statErr)
			}
		}
	})
}

func TestLifecycleLockCanonicalizesStateDirectoryAliases(t *testing.T) {
	base := t.TempDir()
	realState := filepath.Join(base, "real-state")
	mustMkdir(t, realState)
	aliasState := filepath.Join(base, "alias-state")
	createDirectoryJunction(t, aliasState, realState)

	unlock, err := acquireLifecycleLock(context.Background(), realState)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		aliasUnlock, lockErr := acquireLifecycleLock(ctx, aliasState)
		if aliasUnlock != nil {
			aliasUnlock()
		}
		result <- lockErr
	}()
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("state-directory alias bypassed lifecycle lock: %v", err)
	}
}

func TestProvisionRepairsExistingStateFileACLsWithoutChangingBytes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	options := Options{
		RootDir:       root,
		StateDir:      stateDir,
		DataDir:       filepath.Join(stateDir, "data"),
		AppExecutable: appExecutable,
	}
	result, err := Provision(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{result.ConfigPath, result.CredentialsPath} {
		before, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		weakenFileACL(t, path)
		if _, provisionErr := Provision(options); provisionErr != nil {
			t.Fatal(provisionErr)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("ACL repair changed state file bytes: %s", filepath.Base(path))
		}
		secured := readACLFixture(t, path)
		if !secured.Protected || len(secured.SIDs) != 2 || !secured.SIDs[currentUserSID(t)] || !secured.SIDs["S-1-5-18"] {
			t.Fatalf("state file ACL was not repaired to current-user plus LocalSystem only: %s %#v", filepath.Base(path), secured)
		}
	}
}

func TestProvisionRepairsExistingCredentialFileACLWithoutChangingBytes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	options := Options{
		RootDir:       root,
		StateDir:      stateDir,
		DataDir:       filepath.Join(stateDir, "data"),
		AppExecutable: appExecutable,
	}
	result, err := Provision(options)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(result.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	weakenFileACL(t, result.CredentialsPath)
	weak := readACLFixture(t, result.CredentialsPath)
	if weak.Protected || !weak.SIDs["S-1-1-0"] {
		t.Fatalf("credential ACL fixture was not weakened: %#v", weak)
	}

	if _, err := Provision(options); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(result.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("ACL repair changed encrypted credential bytes")
	}
	secured := readACLFixture(t, result.CredentialsPath)
	if !secured.Protected || len(secured.SIDs) != 2 || !secured.SIDs[currentUserSID(t)] || !secured.SIDs["S-1-5-18"] {
		t.Fatalf("credential file ACL was not repaired to current-user plus LocalSystem only: %#v", secured)
	}
}

type aclFixture struct {
	Protected bool
	SIDs      map[string]bool
}

func createDirectoryJunction(t *testing.T, link, target string) {
	t.Helper()
	const script = `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:ASTRORDER_TEST_LINK -Target $env:ASTRORDER_TEST_TARGET | Out-Null`
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	command.Env = append(os.Environ(), "ASTRORDER_TEST_LINK="+link, "ASTRORDER_TEST_TARGET="+target)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if body, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %v: %s", err, body)
	}
}

func pathSDDL(t *testing.T, path string) string {
	t.Helper()
	const script = `(Get-Acl -LiteralPath $env:ASTRORDER_ACL_TEST_PATH).Sddl`
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	command.Env = append(os.Environ(), "ASTRORDER_ACL_TEST_PATH="+path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read isolated ACL SDDL: %v: %s", err, body)
	}
	return string(bytes.TrimSpace(body))
}

func weakenFileACL(t *testing.T, path string) {
	t.Helper()
	for _, arguments := range [][]string{{path, "/inheritance:e"}, {path, "/grant", "*S-1-1-0:(R)"}} {
		command := exec.Command("icacls.exe", arguments...)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if body, err := command.CombinedOutput(); err != nil {
			t.Fatalf("weaken isolated credential ACL: %v: %s", err, body)
		}
	}
}

func readACLFixture(t *testing.T, path string) aclFixture {
	t.Helper()
	const script = `$acl=Get-Acl -LiteralPath $env:ASTRORDER_ACL_TEST_PATH; $rules=@($acl.Access | ForEach-Object { $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value }); [pscustomobject]@{ protected=$acl.AreAccessRulesProtected; sids=$rules } | ConvertTo-Json -Compress -Depth 3`
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	command.Env = append(os.Environ(), "ASTRORDER_ACL_TEST_PATH="+path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read isolated ACL fixture: %v: %s", err, body)
	}
	var raw struct {
		Protected bool     `json:"protected"`
		SIDs      []string `json:"sids"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	result := aclFixture{Protected: raw.Protected, SIDs: make(map[string]bool, len(raw.SIDs))}
	for _, sid := range raw.SIDs {
		result.SIDs[sid] = true
	}
	return result
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
