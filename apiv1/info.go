package apiv1

import (
	"encoding/json"
	"net/http"
)

func (o Options) info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(o)
}
