package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	enrollmentSecretBytes = 32
	nodeIDRandomBytes     = 16
)

func newSecret() (string, error) {
	buf := make([]byte, enrollmentSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random secret bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func newRandomID(prefix string) (string, error) {
	buf := make([]byte, nodeIDRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random ID bytes: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSecret(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("secret is required")
	}
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func secretHashMatches(secret string, storedHash string) (bool, error) {
	expected, err := hashSecret(secret)
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(storedHash)) != 1 {
		return false, nil
	}
	return true, nil
}
