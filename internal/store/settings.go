package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wucm667/sideplane/pkg/protocol"
)

func cloneServerSettings(settings protocol.ServerSettings) protocol.ServerSettings {
	return protocol.ServerSettings{
		ExpectedSidecarVersion:  strings.TrimSpace(settings.ExpectedSidecarVersion),
		ExpectedRuntimeVersions: cloneExpectedRuntimeVersions(settings.ExpectedRuntimeVersions),
	}
}

func cloneExpectedRuntimeVersions(versions map[string]string) map[string]string {
	out := map[string]string{}
	for runtimeType, version := range versions {
		runtimeType = strings.TrimSpace(runtimeType)
		version = strings.TrimSpace(version)
		if runtimeType == "" || version == "" {
			continue
		}
		out[runtimeType] = version
	}
	return out
}

func encodeExpectedRuntimeVersions(versions map[string]string) (string, error) {
	payload, err := json.Marshal(cloneExpectedRuntimeVersions(versions))
	if err != nil {
		return "", fmt.Errorf("encode expected runtime versions: %w", err)
	}
	return string(payload), nil
}

func decodeExpectedRuntimeVersions(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("decode expected runtime versions: %w", err)
	}
	return cloneExpectedRuntimeVersions(decoded), nil
}
