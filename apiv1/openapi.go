package apiv1

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPIDocument []byte

func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Expected GET")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openAPIDocument)
}
