//go:build windows

package lifecycle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPrivateDirAppliesProtectedCurrentUserAndSystemACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := privateDir(path); err != nil {
		t.Fatal(err)
	}
	const script = `$acl=Get-Acl -LiteralPath $env:ASTRORDER_ACL_TEST_PATH; $rules=@($acl.Access | ForEach-Object { [pscustomobject]@{ sid=$_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value; rights=$_.FileSystemRights.ToString(); inheritance=$_.InheritanceFlags.ToString(); inherited=$_.IsInherited; type=$_.AccessControlType.ToString() } }); [pscustomobject]@{ protected=$acl.AreAccessRulesProtected; rules=$rules } | ConvertTo-Json -Compress -Depth 4`
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	command.Env = append(os.Environ(), "ASTRORDER_ACL_TEST_PATH="+path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ACL readback failed: %v: %s", err, body)
	}
	var result struct {
		Protected bool `json:"protected"`
		Rules     []struct {
			SID         string `json:"sid"`
			Rights      string `json:"rights"`
			Inheritance string `json:"inheritance"`
			Inherited   bool   `json:"inherited"`
			Type        string `json:"type"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	userSID, err := user.User.Sid.String()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, rule := range result.Rules {
		if rule.Inherited || rule.Type != "Allow" || rule.Rights != "FullControl" || rule.Inheritance == "None" {
			t.Fatalf("private directory has a weak or inherited ACL rule: %#v", rule)
		}
		seen[rule.SID] = true
	}
	if !result.Protected || len(result.Rules) != 2 || !seen[userSID] || !seen["S-1-5-18"] {
		t.Fatalf("private directory ACL was not current-user plus LocalSystem only: %#v", result)
	}
}
