package apiv1

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DecodeJSON rejects unknown fields and trailing JSON values.
func DecodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err := d.Decode(value)
	if err == nil {
		var extra any
		if d.Decode(&extra) == io.EOF {
			return true
		}
	}
	code := "invalid_json"
	if err != nil && strings.HasPrefix(err.Error(), "json: unknown field ") {
		code = "unknown_field"
	}
	WriteProblem(w, 400, code, "Expected one JSON value with known fields")
	return false
}
