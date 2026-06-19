package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOperatorTokenRejectsRequestsWhenNotConfigured(t *testing.T) {
	token := NewOperatorToken("", false)

	if token.AuthorizeHeader("") {
		t.Fatalf("unconfigured operator token authorized empty header")
	}
}

func TestOperatorTokenAllowsExplicitUnauthenticatedDevMode(t *testing.T) {
	token := NewOperatorToken("", true)

	if !token.AuthorizeHeader("") {
		t.Fatalf("explicit unauthenticated dev mode rejected empty header")
	}
}

func TestOperatorTokenRequiresMatchingBearerToken(t *testing.T) {
	token := NewOperatorToken("dev-token", true)

	if token.AuthorizeHeader("") {
		t.Fatalf("missing bearer token authorized")
	}
	if token.AuthorizeHeader("Bearer wrong-token") {
		t.Fatalf("wrong bearer token authorized")
	}
	if !token.AuthorizeHeader("Bearer dev-token") {
		t.Fatalf("matching bearer token rejected")
	}
}

func TestOperatorTokenBootstrapBypassesNamedVerifier(t *testing.T) {
	verifier := &fakeOperatorTokenVerifier{acceptToken: "named-token", tokenID: "optok_named"}
	token := NewOperatorTokenWithVerifier("dev-token", false, verifier)

	if !token.AuthorizeHeader("Bearer dev-token") {
		t.Fatalf("bootstrap token rejected")
	}
	if verifier.verifyCalls != 0 {
		t.Fatalf("named verifier called for bootstrap token")
	}
}

func TestOperatorTokenAcceptsNamedTokenAndUpdatesLastUsed(t *testing.T) {
	verifier := &fakeOperatorTokenVerifier{acceptToken: "named-token", tokenID: "optok_named"}
	token := NewOperatorTokenWithVerifier("dev-token", false, verifier)
	usedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	token.now = func() time.Time { return usedAt }

	if !token.AuthorizeHeader("Bearer named-token") {
		t.Fatalf("named operator token rejected")
	}
	if verifier.updatedID != "optok_named" || !verifier.updatedAt.Equal(usedAt) {
		t.Fatalf("last used update = id:%q at:%s, want optok_named/%s", verifier.updatedID, verifier.updatedAt, usedAt)
	}
}

func TestOperatorTokenIgnoresNamedTokenLastUsedFailure(t *testing.T) {
	verifier := &fakeOperatorTokenVerifier{
		acceptToken: "named-token",
		tokenID:     "optok_named",
		updateErr:   errors.New("store unavailable"),
	}
	token := NewOperatorTokenWithVerifier("", false, verifier)

	if !token.AuthorizeHeader("Bearer named-token") {
		t.Fatalf("named operator token rejected when last-used update failed")
	}
}

type fakeOperatorTokenVerifier struct {
	acceptToken string
	tokenID     string
	updateErr   error
	verifyCalls int
	updatedID   string
	updatedAt   time.Time
}

func (v *fakeOperatorTokenVerifier) VerifyOperatorToken(_ context.Context, token string) (string, bool, error) {
	v.verifyCalls++
	if token != v.acceptToken {
		return "", false, nil
	}
	return v.tokenID, true, nil
}

func (v *fakeOperatorTokenVerifier) UpdateOperatorTokenLastUsed(_ context.Context, tokenID string, usedAt time.Time) error {
	v.updatedID = tokenID
	v.updatedAt = usedAt
	return v.updateErr
}
