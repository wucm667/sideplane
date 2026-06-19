package protocol

// APIError is the structured JSON error shape returned by API endpoints.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
