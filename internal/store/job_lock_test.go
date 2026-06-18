package store

import (
	"encoding/json"
	"testing"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func configApplyPayloadForTest(t *testing.T, runtimeType string, configPath string) string {
	t.Helper()
	payload, err := json.Marshal(protocol.SignedConfigPlan{
		Plan: protocol.ConfigPlan{
			Body: protocol.ConfigPlanBody{
				RuntimeType: runtimeType,
				Profile:     configPath,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal config apply payload: %v", err)
	}
	return string(payload)
}
