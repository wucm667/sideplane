package auth

import (
	"crypto/subtle"
	"strings"
)

// OperatorTokenEnv is the environment variable used to configure operator auth.
const OperatorTokenEnv = "SIDEPLANE_OPERATOR_TOKEN"

// OperatorToken authorizes mutating operator API requests.
type OperatorToken struct {
	token string
}

// NewOperatorToken returns an optional operator token authorizer.
func NewOperatorToken(token string) OperatorToken {
	return OperatorToken{token: strings.TrimSpace(token)}
}

// Configured reports whether operator auth should be enforced.
func (t OperatorToken) Configured() bool {
	return t.token != ""
}

// AuthorizeHeader reports whether an Authorization header matches the token.
func (t OperatorToken) AuthorizeHeader(authorization string) bool {
	if !t.Configured() {
		return true
	}
	credential, ok := BearerToken(authorization)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(credential), []byte(t.token)) == 1
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
