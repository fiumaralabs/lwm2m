package apiv1

import (
	"encoding/json"
	"errors"
	"github.com/fiumaralabs/lwm2m/store"
	"mime"
	"net/http"
)

func (o Options) settings(w http.ResponseWriter, r *http.Request) {
	RequireScope("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "PATCH" {
			w.Header().Set("Allow", "GET, PATCH")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET or PATCH")
			return
		}
		tenant := TenantFrom(r.Context())
		var v store.SettingsValue
		var err error
		if r.Method == "PATCH" {
			typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if e != nil || typ != "application/merge-patch+json" {
				WriteProblem(w, 415, "unsupported_media_type", "Expected application/merge-patch+json")
				return
			}
			var patch json.RawMessage
			if !DecodeJSON(w, r, &patch) {
				return
			}
			v, err = o.Settings.Patch(tenant, patch, webhookIfMatch(r))
		} else {
			v, err = o.Settings.Get(tenant)
		}
		if errors.Is(err, store.ErrSettingsPrecondition) {
			WriteProblem(w, 412, "precondition_failed", "Settings precondition failed")
			return
		}
		if errors.Is(err, store.ErrSettingsInvalid) {
			WriteProblem(w, 400, "invalid_settings", "Invalid settings patch")
			return
		}
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Settings request failed")
			return
		}
		w.Header().Set("ETag", v.ETag())
		deviceJSON(w, 200, v)
	})).ServeHTTP(w, r)
}
