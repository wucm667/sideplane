package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/wucm667/sideplane/pkg/protocol"
)

const desiredConfigHistoryActorOperator = "operator"

func newDesiredConfigHistoryEntry(desired protocol.DesiredConfig, actor string, updatedAt time.Time) (protocol.DesiredConfigHistoryEntry, error) {
	id, err := newRandomID("deshist_")
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, err
	}
	config, err := cloneDesiredConfigJSON(desired)
	if err != nil {
		return protocol.DesiredConfigHistoryEntry{}, err
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = desiredConfigHistoryActorOperator
	}
	return protocol.DesiredConfigHistoryEntry{
		ID:          id,
		Config:      config,
		DesiredHash: hashDesiredConfig(config),
		UpdatedAt:   updatedAt.UTC(),
		Actor:       actor,
	}, nil
}

func cloneDesiredConfigJSON(desired protocol.DesiredConfig) (protocol.DesiredConfig, error) {
	payload, err := json.Marshal(desired)
	if err != nil {
		return protocol.DesiredConfig{}, err
	}
	var clone protocol.DesiredConfig
	if err := json.Unmarshal(payload, &clone); err != nil {
		return protocol.DesiredConfig{}, err
	}
	return clone, nil
}

func hashDesiredConfig(desired protocol.DesiredConfig) string {
	payload, _ := json.Marshal(desired)
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
