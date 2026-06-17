package protocol

import "time"

// CreateEnrollmentTokenRequest asks the server to mint a one-time enrollment token.
type CreateEnrollmentTokenRequest struct {
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// CreateEnrollmentTokenResponse returns the plaintext token once and its expiry.
type CreateEnrollmentTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// EnrollNodeRequest exchanges a one-time enrollment token for node credentials.
type EnrollNodeRequest struct {
	Token          string `json:"token"`
	NodeID         string `json:"nodeId,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
	SidecarVersion string `json:"sidecarVersion,omitempty"`
}

// EnrollNodeResponse returns the node identity and plaintext credential once.
type EnrollNodeResponse struct {
	NodeID         string `json:"nodeId"`
	NodeCredential string `json:"nodeCredential"`
}
