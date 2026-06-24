package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	secretKeySize   = 32
	secretNonceSize = 12
)

// DeriveSecretKey derives a 32-byte AES-256 key from an operator-provided value.
// Empty input means provider secret storage is not configured.
func DeriveSecretKey(value string) []byte {
	if value == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// Encrypt seals plaintext with AES-256-GCM and returns nonce || ciphertext.
func Encrypt(key []byte, plaintext []byte) ([]byte, error) {
	if len(key) != secretKeySize {
		return nil, fmt.Errorf("secret key length = %d, want %d", len(key), secretKeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCMWithNonceSize(block, secretNonceSize)
	if err != nil {
		return nil, fmt.Errorf("create GCM cipher: %w", err)
	}
	nonce := make([]byte, secretNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read random nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, nil)
	blob := make([]byte, 0, len(nonce)+len(sealed))
	blob = append(blob, nonce...)
	blob = append(blob, sealed...)
	return blob, nil
}

// Decrypt opens a nonce || ciphertext blob produced by Encrypt.
func Decrypt(key []byte, blob []byte) ([]byte, error) {
	if len(key) != secretKeySize {
		return nil, fmt.Errorf("secret key length = %d, want %d", len(key), secretKeySize)
	}
	if len(blob) < secretNonceSize {
		return nil, errors.New("encrypted secret is too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCMWithNonceSize(block, secretNonceSize)
	if err != nil {
		return nil, fmt.Errorf("create GCM cipher: %w", err)
	}
	plaintext, err := aead.Open(nil, blob[:secretNonceSize], blob[secretNonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return plaintext, nil
}
