package apiv1

import (
	"encoding/json"
	"net/http"
)

func (o Options) info(w http.ResponseWriter, r *http.Request) {
	caps := make(map[string]bool, len(o.Capabilities)+1)
	for key, enabled := range o.Capabilities {
		caps[key] = enabled
	}
	caps["timeseries"] = o.TimeSeries != nil
	o.Capabilities = caps
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(o)
}
