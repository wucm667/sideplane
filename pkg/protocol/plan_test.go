package protocol

import (
	"encoding/json"
	"testing"
	"time"

	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
)

func TestSignAndVerifyConfigPlan(t *testing.T) {
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	plan := testConfigPlan()
	signedPlan, err := SignConfigPlan(plan, keyPair.PrivateKey)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}
	if err := VerifySignedConfigPlan(signedPlan, keyPair.PublicKey); err != nil {
		t.Fatalf("verify plan: %v", err)
	}
}

func TestVerifyConfigPlanRejectsTamperAndWrongKey(t *testing.T) {
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	wrongKeyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong keypair: %v", err)
	}
	signedPlan, err := SignConfigPlan(testConfigPlan(), keyPair.PrivateKey)
	if err != nil {
		t.Fatalf("sign plan: %v", err)
	}
	tampered := signedPlan
	tampered.Plan.Body.Desired.Model = "gpt-5-tampered"
	if err := VerifySignedConfigPlan(tampered, keyPair.PublicKey); err == nil {
		t.Fatalf("tampered plan verified")
	}
	if err := VerifySignedConfigPlan(signedPlan, wrongKeyPair.PublicKey); err == nil {
		t.Fatalf("wrong key verified")
	}
}

func TestCanonicalConfigPlanBytesDeterministic(t *testing.T) {
	first, err := CanonicalConfigPlanBytes(testConfigPlan())
	if err != nil {
		t.Fatalf("canonical first: %v", err)
	}
	second, err := CanonicalConfigPlanBytes(testConfigPlan())
	if err != nil {
		t.Fatalf("canonical second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical bytes differ:\n%s\n%s", first, second)
	}
	var decoded map[string]any
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("canonical JSON invalid: %v", err)
	}
}

func testConfigPlan() ConfigPlan {
	return ConfigPlan{
		ID:           "plan_123",
		Schema:       ConfigPlanSchema,
		Version:      ConfigPlanVersion,
		CreatedAt:    time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC),
		TargetNodeID: "node-a",
		Mode:         ConfigPlanModeDryRun,
		Body: ConfigPlanBody{
			RuntimeType: "hermes",
			Profile:     "default",
			Desired:     ProviderModelConfig{Provider: "openai", Model: "gpt-5.2"},
			DryRun:      true,
		},
	}
}
