package credentials

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
)

func TestCurrentUserDPAPIRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real DPAPI is available only on Windows")
	}
	plain := []byte("isolated-dpapi-round-trip")
	encrypted, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, plain) {
		t.Fatal("DPAPI returned plaintext")
	}
	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatal("DPAPI round-trip changed the value")
	}
}

func TestDPAPIRejectsInvalidCiphertext(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real DPAPI is available only on Windows")
	}
	if _, err := Decrypt([]byte("not-dpapi")); err == nil {
		t.Fatal("invalid ciphertext was accepted")
	}
}

func TestSupportedMatchesPlatform(t *testing.T) {
	if Supported() != (runtime.GOOS == "windows") {
		t.Fatalf("Supported() = %v on %s", Supported(), runtime.GOOS)
	}
}

func TestUnsupportedPlatformErrorIsStable(t *testing.T) {
	if ErrUnsupportedPlatform == nil || !errors.Is(ErrUnsupportedPlatform, ErrUnsupportedPlatform) {
		t.Fatal("unsupported-platform policy is not explicit")
	}
}
