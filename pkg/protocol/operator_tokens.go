package protocol

import "time"

// OperatorToken is metadata for a named operator API token. The plaintext token
// is only returned once by CreateOperatorTokenResponse.
type OperatorToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// CreateOperatorTokenRequest creates a named, revocable operator API token.
type CreateOperatorTokenRequest struct {
	Name string `json:"name"`
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
