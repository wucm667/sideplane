package crypto

import (
	"path/filepath"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	payload := []byte(`{"planId":"plan_1"}`)
	signature, err := Sign(keyPair.PrivateKey, payload)
	if err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	if err := Verify(keyPair.PublicKey, payload, signature); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
}

func TestVerifyRejectsTamperAndWrongKey(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	wrongKeyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong keypair: %v", err)
	}
	payload := []byte(`{"planId":"plan_1"}`)
	signature, err := Sign(keyPair.PrivateKey, payload)
	if err != nil {
		t.Fatalf("sign payload: %v", err)
	}
	if err := Verify(keyPair.PublicKey, []byte(`{"planId":"plan_2"}`), signature); err == nil {
		t.Fatalf("tampered payload verified")
	}
	if err := Verify(wrongKeyPair.PublicKey, payload, signature); err == nil {
		t.Fatalf("wrong key verified")
	}
}

func TestLoadOrCreateKeyPairPersistsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing-key.json")
	first, err := LoadOrCreateKeyPair(path)
	if err != nil {
		t.Fatalf("load/create first keypair: %v", err)
	}
	second, err := LoadOrCreateKeyPair(path)
	if err != nil {
		t.Fatalf("load second keypair: %v", err)
	}
	if PublicKeyString(first.PublicKey) != PublicKeyString(second.PublicKey) {
		t.Fatalf("public key changed after reload")
	}
}
