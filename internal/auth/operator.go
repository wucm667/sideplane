package auth

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"
)

// OperatorTokenEnv is the environment variable used to configure operator auth.
const OperatorTokenEnv = "SIDEPLANE_OPERATOR_TOKEN"

// AllowUnauthenticatedOperatorAPIEnv explicitly enables unauthenticated
// mutating operator APIs for local development.
const AllowUnauthenticatedOperatorAPIEnv = "SIDEPLANE_ALLOW_UNAUTHENTICATED_OPERATOR_API"

// OperatorToken authorizes mutating operator API requests.
type OperatorToken struct {
	token                string
	allowUnauthenticated bool
	verifier             OperatorTokenVerifier
	now                  func() time.Time
}

// OperatorTokenVerifier verifies named operator tokens stored outside auth.
type OperatorTokenVerifier interface {
	VerifyOperatorToken(ctx context.Context, token string) (string, bool, error)
	UpdateOperatorTokenLastUsed(ctx context.Context, tokenID string, usedAt time.Time) error
}

// NewOperatorToken returns an operator token authorizer.
func NewOperatorToken(token string, allowUnauthenticated bool) OperatorToken {
	return NewOperatorTokenWithVerifier(token, allowUnauthenticated, nil)
}

// NewOperatorTokenWithVerifier returns an operator token authorizer that also
// accepts active named tokens from verifier.
func NewOperatorTokenWithVerifier(token string, allowUnauthenticated bool, verifier OperatorTokenVerifier) OperatorToken {
	return OperatorToken{
		token:                strings.TrimSpace(token),
		allowUnauthenticated: allowUnauthenticated,
		verifier:             verifier,
		now:                  time.Now,
	}
}

// Configured reports whether operator auth should be enforced.
func (t OperatorToken) Configured() bool {
	return t.token != "" || t.verifier != nil
}

// AuthorizeHeader reports whether an Authorization header matches the token.
func (t OperatorToken) AuthorizeHeader(authorization string) bool {
	return t.AuthorizeHeaderContext(context.Background(), authorization)
}

// AuthorizeHeaderContext reports whether an Authorization header matches the
// bootstrap token or an active named token.
func (t OperatorToken) AuthorizeHeaderContext(ctx context.Context, authorization string) bool {
	if t.token == "" && t.allowUnauthenticated {
		return true
	}
	if !t.Configured() {
		return false
	}
	credential, ok := BearerToken(authorization)
	if !ok {
		return false
	}
	if t.token != "" && subtle.ConstantTimeCompare([]byte(credential), []byte(t.token)) == 1 {
		return true
	}
	if t.verifier == nil {
		return false
	}
	tokenID, ok, err := t.verifier.VerifyOperatorToken(ctx, credential)
	if err != nil || !ok {
		return false
	}
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	_ = t.verifier.UpdateOperatorTokenLastUsed(ctx, tokenID, now().UTC())
	return true
}

// BearerToken extracts a bearer token from an Authorization header.
func BearerToken(authorization string) (string, bool) {
	fields := strings.Fields(authorization)
	if len(fields) != 2 {
		return "", false
	}
	if !strings.EqualFold(fields[0], "Bearer") {
		return "", false
	}
	credential := strings.TrimSpace(fields[1])
	if credential == "" {
		return "", false
	}
	return credential, true
}
