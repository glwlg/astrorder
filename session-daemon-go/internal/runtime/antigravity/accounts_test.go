package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"astrorder.dev/contracts/agycredential"
)

func TestAgyVaultCredentialAcrossIndependentHomes(t *testing.T) {
	ctx := context.Background()
	firstHome, secondHome := t.TempDir(), t.TempDir()
	credential := []byte("{\n \"token\": {\"access_token\":\"synthetic-one\"}, \"id_token_email\":\"a@example.test\"}\n")
	outgoing := []byte(`{"token":{"access_token":"synthetic-outgoing"},"id_token_email":"other@example.test"}`)
	activePath := func(home string) string { return filepath.Join(home, ".gemini", "antigravity-cli", agyTokenName) }
	firstPath, secondPath := activePath(firstHome), activePath(secondHome)
	if err := os.MkdirAll(filepath.Dir(firstPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPath, outgoing, 0600); err != nil {
		t.Fatal(err)
	}

	// Exercise the JSON representation the SSH helper receives over stdin.
	invoke := func(home, action string, token []byte) (map[string]any, error) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"home": home, "action": action, "account_id": "selected", "credential": token})
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		return ExecuteHelper(ctx, request)
	}
	if _, err := invoke(firstHome, "account.vault.check", []byte("invalid")); err == nil {
		t.Fatal("accepted an invalid credential")
	}
	for _, home := range []string{firstHome, secondHome} {
		result, err := invoke(home, "account.vault.check", credential)
		if err != nil || result["ready"] != true {
			t.Fatalf("credential preflight failed: %v", err)
		}
	}
	if current, err := os.ReadFile(firstPath); err != nil || !bytes.Equal(current, outgoing) {
		t.Fatal("preflight changed existing login")
	}
	if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight created an active credential on a fresh target")
	}
	identity, _ := agycredential.Identity(credential)
	for _, home := range []string{firstHome, secondHome} {
		result, err := invoke(home, "account.vault.apply", credential)
		if err != nil || result["account_id"] != "selected" {
			t.Fatalf("apply credential failed: %v", err)
		}
		result, err = invoke(home, "account.vault.state", nil)
		if err != nil || result["identity"] != identity || result["credential"] != nil {
			t.Fatal("incorrect active identity after apply")
		}
		stored, err := os.ReadFile(activePath(home))
		if err != nil || !bytes.Equal(stored, credential) {
			t.Fatal("applied credential differs from input bytes")
		}
	}
	result, err := invoke(secondHome, "account.vault.capture", nil)
	captured, _ := result["credential"].([]byte)
	if err != nil || !bytes.Equal(captured, credential) {
		t.Fatal("capture changed the credential")
	}
	_, inactive, err := agyAccountOperation(ctx, firstHome, "account.vault.quota", "other", outgoing)
	if err != nil || !bytes.Equal(inactive, outgoing) {
		t.Fatal("quota selected the active credential instead of the requested account")
	}
	stored, err := os.ReadFile(firstPath)
	if err != nil || !bytes.Equal(stored, credential) {
		t.Fatal("quota selection changed active login")
	}
}
