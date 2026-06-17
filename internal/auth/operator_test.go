package auth

import "testing"

func TestOperatorTokenAllowsRequestsWhenNotConfigured(t *testing.T) {
	token := NewOperatorToken("")

	if !token.AuthorizeHeader("") {
		t.Fatalf("unconfigured operator token rejected empty header")
	}
}

func TestOperatorTokenRequiresMatchingBearerToken(t *testing.T) {
	token := NewOperatorToken("dev-token")

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
