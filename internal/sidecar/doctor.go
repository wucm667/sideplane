package sidecar

import (
	"fmt"
	"os"
	"strings"
)

// PathStatus reports whether a configured local path can be read without
// exposing file contents.
type PathStatus struct {
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
	Error    string `json:"error,omitempty"`
}

// CheckReadablePaths validates configured paths without reading or returning
// their contents.
func CheckReadablePaths(paths []string) []PathStatus {
	statuses := make([]PathStatus, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		status := PathStatus{Path: path}
		info, err := os.Stat(path)
		if err != nil {
			status.Error = fmt.Sprintf("stat: %v", err)
			statuses = append(statuses, status)
			continue
		}
		if !info.Mode().IsRegular() {
			status.Error = "not a regular file"
			statuses = append(statuses, status)
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			status.Error = fmt.Sprintf("open: %v", err)
			statuses = append(statuses, status)
			continue
		}
		_ = file.Close()
		status.Readable = true
		statuses = append(statuses, status)
	}
	return statuses
}
