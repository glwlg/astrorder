package agyaccounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialVaultPersistenceAndValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	vault := New(dir)
	for _, invalid := range [][]byte{
		[]byte(`{"token":{}}`),
		[]byte(`{"token":{"access_token":""},"id_token_email":"a@example.test"}`),
		[]byte(`{"token":{"access_token":1}}`),
		bytes.Repeat([]byte{'x'}, 512*1024+1),
	} {
		if _, err := vault.Save("账号", invalid); !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("expected invalid credential, got %v", err)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid import initialized the vault")
	}
	original := []byte("{\r\n\"token\":{\"access_token\":\"synthetic-credential\"},\"id_token_email\":\"a@example.test\"\r\n}\r\n")
	id, err := vault.Save("测试账号", original)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, id+".sealed"))
	if err != nil || bytes.Contains(blob, []byte("synthetic-credential")) || bytes.Contains(blob, []byte("测试账号")) {
		t.Fatal("credential record was not sealed")
	}
	vault = New(dir)
	stored, err := vault.Credential(id)
	if err != nil || !bytes.Equal(stored, original) {
		t.Fatal("credential was not stored byte-for-byte")
	}
	refreshed := []byte(`{"token":{"refresh_token":"new-synthetic-credential"},"id_token_email":"A@example.test"}`)
	newID, err := vault.Save("更新后的账号", refreshed)
	if err != nil || newID != id {
		t.Fatal("reimporting the same account changed the saved account ID")
	}
	items, err := vault.List()
	if err != nil || len(items) != 1 || items[0].ID != id || items[0].Name != "更新后的账号" {
		t.Fatal("vault metadata did not reflect the new credential")
	}
	stored, err = vault.Credential(id)
	if err != nil || !bytes.Equal(stored, refreshed) {
		t.Fatal("reimport did not update the original credential")
	}
}

func TestImportLegacyAccountSnapshot(t *testing.T) {
	token := []byte("{\n \"token\": {\"refresh_token\":\"fixture\"}\n}\n")
	legacy, err := json.Marshal(record{Name: "历史账号", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ImportCredential(legacy)
	if err != nil || !bytes.Equal(imported, token) {
		t.Fatal("legacy snapshot did not preserve the original token")
	}
}
