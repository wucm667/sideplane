package protocol

import (
	"encoding/json"
	"fmt"
	"time"

	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
)

const (
	// ConfigPlanSchema is the schema identifier for signed config plans.
	ConfigPlanSchema = "sideplane.config-plan.v1"
	// ConfigPlanVersion is the supported signed config plan version.
	ConfigPlanVersion = 1
	// ConfigPlanModeDryRun validates and reports without replacing live config.
	ConfigPlanModeDryRun = "dry_run"
	// ConfigPlanModeLive enables the live branch when sidecar policy permits it.
	ConfigPlanModeLive = "live"
)

// ConfigPlanBody is the MVP signed plan body for provider/model changes.
type ConfigPlanBody struct {
	RuntimeType string               `json:"runtimeType"`
	Profile     string               `json:"profile,omitempty"`
	Desired     ProviderModelConfig  `json:"desired"`
	Providers   []ProviderDefinition `json:"providers,omitempty"`
	DryRun      bool                 `json:"dryRun"`
}

// ConfigPlan is the canonical payload that is signed by the server.
type ConfigPlan struct {
	ID           string         `json:"id"`
	Schema       string         `json:"schema"`
	Version      int            `json:"version"`
	CreatedAt    time.Time      `json:"createdAt"`
	TargetNodeID string         `json:"targetNodeId"`
	Mode         string         `json:"mode"`
	Body         ConfigPlanBody `json:"body"`
}

// SignedConfigPlan wraps a config plan with its base64 ed25519 signature.
type SignedConfigPlan struct {
	Plan      ConfigPlan `json:"plan"`
	Signature string     `json:"signature"`
}

// ConfigApplySecretsResponse returns provider secret values to an authenticated
// sidecar just-in-time for a verified config apply plan.
type ConfigApplySecretsResponse struct {
	Secrets map[string]string `json:"secrets"`
}

// PublicSigningKeyResponse exposes the server public signing key to sidecars.
type PublicSigningKeyResponse struct {
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
}

// CanonicalConfigPlanBytes returns the deterministic serialization to sign.
func CanonicalConfigPlanBytes(plan ConfigPlan) ([]byte, error) {
	if plan.CreatedAt.IsZero() {
		return nil, fmt.Errorf("createdAt is required")
	}
	plan.CreatedAt = plan.CreatedAt.UTC()
	payload, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal config plan: %w", err)
	}
	return payload, nil
}

// SignConfigPlan signs a config plan.
func SignConfigPlan(plan ConfigPlan, privateKey []byte) (SignedConfigPlan, error) {
	payload, err := CanonicalConfigPlanBytes(plan)
	if err != nil {
		return SignedConfigPlan{}, err
	}
	signature, err := spcrypto.Sign(privateKey, payload)
	if err != nil {
		return SignedConfigPlan{}, err
	}
	return SignedConfigPlan{Plan: plan, Signature: signature}, nil
}

// VerifySignedConfigPlan verifies a signed config plan.
func VerifySignedConfigPlan(signedPlan SignedConfigPlan, publicKey []byte) error {
	payload, err := CanonicalConfigPlanBytes(signedPlan.Plan)
	if err != nil {
		return err
	}
	return spcrypto.Verify(publicKey, payload, signedPlan.Signature)
}
