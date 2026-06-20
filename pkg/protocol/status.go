package protocol

// WhoamiResponse describes the authenticated operator identity without
// exposing any bearer secret.
type WhoamiResponse struct {
	Scope     OperatorTokenScope `json:"scope"`
	TokenName string             `json:"tokenName"`
}

// ServerStatusResponse is a cheap control-plane status summary.
type ServerStatusResponse struct {
	Version       string `json:"version"`
	Commit        string `json:"commit,omitempty"`
	BuildDate     string `json:"buildDate,omitempty"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	SchemaVersion int    `json:"schemaVersion"`
	NodeCount     int    `json:"nodeCount"`
	RolloutCount  int    `json:"rolloutCount"`
}
