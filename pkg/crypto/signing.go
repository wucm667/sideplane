package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyPair contains an ed25519 signing keypair.
type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateKeyPair creates a new ed25519 signing keypair.
func GenerateKeyPair() (KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate signing keypair: %w", err)
	}
	return KeyPair{PublicKey: publicKey, PrivateKey: privateKey}, nil
}

// PublicKeyString returns a base64-encoded public key.
func PublicKeyString(publicKey ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(publicKey)
}

// ParsePublicKey parses a base64-encoded ed25519 public key.
func ParsePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key length = %d, want %d", len(decoded), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

// Sign signs payload bytes with privateKey and returns a base64 signature.
func Sign(privateKey ed25519.PrivateKey, payload []byte) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("private key length = %d, want %d", len(privateKey), ed25519.PrivateKeySize)
	}
	signature := ed25519.Sign(privateKey, payload)
	return base64.StdEncoding.EncodeToString(signature), nil
}

// Verify verifies a base64 signature against payload bytes.
func Verify(publicKey ed25519.PublicKey, payload []byte, signature string) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("public key length = %d, want %d", len(publicKey), ed25519.PublicKeySize)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(publicKey, payload, decoded) {
		return errors.New("signature verification failed")
	}
	return nil
}

type persistedKey struct {
	PrivateKey string `json:"privateKey"`
}

// LoadOrCreateKeyPair loads a private signing key from path or creates one.
func LoadOrCreateKeyPair(path string) (KeyPair, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return GenerateKeyPair()
	}

	contents, err := os.ReadFile(path)
	if err == nil {
		var persisted persistedKey
		if err := json.Unmarshal(contents, &persisted); err != nil {
			return KeyPair{}, fmt.Errorf("parse signing key file: %w", err)
		}
		privateKeyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(persisted.PrivateKey))
		if err != nil {
			return KeyPair{}, fmt.Errorf("decode private key: %w", err)
		}
		if len(privateKeyBytes) != ed25519.PrivateKeySize {
			return KeyPair{}, fmt.Errorf("private key length = %d, want %d", len(privateKeyBytes), ed25519.PrivateKeySize)
		}
		privateKey := ed25519.PrivateKey(privateKeyBytes)
		return KeyPair{PublicKey: privateKey.Public().(ed25519.PublicKey), PrivateKey: privateKey}, nil
	}
	if !os.IsNotExist(err) {
		return KeyPair{}, fmt.Errorf("read signing key file: %w", err)
	}

	keyPair, err := GenerateKeyPair()
	if err != nil {
		return KeyPair{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return KeyPair{}, fmt.Errorf("create signing key directory: %w", err)
	}
	payload, err := json.MarshalIndent(persistedKey{PrivateKey: base64.StdEncoding.EncodeToString(keyPair.PrivateKey)}, "", "  ")
	if err != nil {
		return KeyPair{}, fmt.Errorf("marshal signing key: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return KeyPair{}, fmt.Errorf("write signing key file: %w", err)
	}
	return keyPair, nil
}
