package apiv1

import (
	"encoding/json"
	"net/http"
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail"`
	Code   string       `json:"code"`
	Errors []FieldError `json:"errors,omitempty"`
}

// WriteProblem writes an RFC 9457 error without credential details.
func WriteProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	if status == 401 {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	// RFC 9457's about:blank type describes the HTTP status without a product domain.
	// Code carries the more specific machine-readable error identifier.
	_ = json.NewEncoder(w).Encode(Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code})
}
