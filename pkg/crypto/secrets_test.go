package crypto

import (
	"bytes"
	"testing"
)

func TestDeriveSecretKeyEmptyMeansUnset(t *testing.T) {
	if key := DeriveSecretKey(""); key != nil {
		t.Fatalf("empty secret key = %#v, want nil", key)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveSecretKey("operator master secret")
	plaintext := []byte("sk-test-provider-key")
	blob, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatalf("ciphertext contains plaintext")
	}
	got, err := Decrypt(key, blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext = %q, want %q", got, plaintext)
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	blob, err := Encrypt(DeriveSecretKey("right"), []byte("sk-test-provider-key"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := Decrypt(DeriveSecretKey("wrong"), blob); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
}

func TestDecryptRejectsTamperedBlob(t *testing.T) {
	key := DeriveSecretKey("operator master secret")
	blob, err := Encrypt(key, []byte("sk-test-provider-key"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := Decrypt(key, blob); err == nil {
		t.Fatal("decrypt of tampered blob succeeded")
	}
}

func TestSecretCryptoRejectsBadKeyLength(t *testing.T) {
	if _, err := Encrypt(nil, []byte("secret")); err == nil {
		t.Fatal("encrypt with nil key succeeded")
	}
	if _, err := Encrypt([]byte("short"), []byte("secret")); err == nil {
		t.Fatal("encrypt with short key succeeded")
	}
	if _, err := Decrypt(nil, []byte("ciphertext")); err == nil {
		t.Fatal("decrypt with nil key succeeded")
	}
	if _, err := Decrypt([]byte("short"), []byte("ciphertext")); err == nil {
		t.Fatal("decrypt with short key succeeded")
	}
}
