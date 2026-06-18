package auth

import "testing"

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
