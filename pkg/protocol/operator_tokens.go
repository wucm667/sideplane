package protocol

import "time"

// OperatorTokenScope is the authorization scope granted to a named operator
// token. Only two scopes exist: admin (full access) and readonly (GET/list
// endpoints only; mutating endpoints return 403).
type OperatorTokenScope string

const (
	// OperatorTokenScopeAdmin grants full read and mutating access. The
	// env/flag bootstrap token is always admin.
	OperatorTokenScopeAdmin OperatorTokenScope = "admin"
	// OperatorTokenScopeReadonly grants read-only access; mutating endpoints
	// return 403.
	OperatorTokenScopeReadonly OperatorTokenScope = "readonly"
)

// NormalizeOperatorTokenScope defaults an empty scope to admin (preserving
// backward compatibility) and reports whether the value is a known scope.
func NormalizeOperatorTokenScope(scope OperatorTokenScope) (OperatorTokenScope, bool) {
	switch scope {
	case "", OperatorTokenScopeAdmin:
		return OperatorTokenScopeAdmin, true
	case OperatorTokenScopeReadonly:
		return OperatorTokenScopeReadonly, true
	default:
		return scope, false
	}
}

// OperatorToken is metadata for a named operator API token. The plaintext token
// is only returned once by CreateOperatorTokenResponse.
type OperatorToken struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Scope      OperatorTokenScope `json:"scope"`
	CreatedAt  time.Time          `json:"createdAt"`
	LastUsedAt *time.Time         `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time         `json:"revokedAt,omitempty"`
}

// CreateOperatorTokenRequest creates a named, revocable operator API token.
type CreateOperatorTokenRequest struct {
	Name  string             `json:"name"`
	Scope OperatorTokenScope `json:"scope,omitempty"`
}

// CreateOperatorTokenResponse returns a plaintext token once alongside metadata.
type CreateOperatorTokenResponse struct {
	OperatorToken OperatorToken `json:"operatorToken"`
	Token         string        `json:"token"`
}

// ListOperatorTokensResponse returns operator token metadata only.
type ListOperatorTokensResponse struct {
	Tokens []OperatorToken `json:"tokens"`
}

// RevokeOperatorTokenResponse returns metadata for the revoked operator token.
type RevokeOperatorTokenResponse struct {
	OperatorToken OperatorToken `json:"operatorToken"`
}
