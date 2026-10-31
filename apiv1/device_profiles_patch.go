package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
)

// profileMerge applies RFC 7396 without converting JSON numbers to float64.
func profileMerge(target, patch json.RawMessage) json.RawMessage {
	var changes map[string]json.RawMessage
	if json.Unmarshal(patch, &changes) != nil || changes == nil {
		return patch
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(target, &object) != nil || object == nil {
		object = make(map[string]json.RawMessage)
	}
	for key, value := range changes {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(object, key)
		} else {
			object[key] = profileMerge(object[key], value)
		}
	}
	result, _ := json.Marshal(object)
	return result
}

func (o Options) patchDeviceProfile(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/v1/device-profiles/")
	tenant := TenantFrom(r.Context())
	if key == "" || strings.Contains(key, "/") {
		WriteProblem(w, 404, "not_found", "Unknown profile")
		return
	}
	current, found := o.DeviceProfiles.Get(tenant, key)
	if !found {
		WriteProblem(w, 404, "not_found", "Unknown profile")
		return
	}
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/merge-patch+json" {
		WriteProblem(w, 415, "unsupported_media_type", "Expected application/merge-patch+json")
		return
	}
	var patch json.RawMessage
	if !DecodeJSON(w, r, &patch) {
		return
	}
	match := webhookIfMatch(r)
	for {
		body, err := json.Marshal(current)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to encode profile")
			return
		}
		sum := sha256.Sum256(body)
		tag := fmt.Sprintf("\"profile-v1-%x\"", sum)
		matched := !match.Required || match.Any
		for _, candidate := range match.Tags {
			matched = matched || candidate == tag
		}
		if !matched {
			WriteProblem(w, 412, "precondition_failed", "Profile precondition failed")
			return
		}
		merged := profileMerge(body, patch)
		var updated *DeviceProfile
		d := json.NewDecoder(bytes.NewReader(merged))
		d.DisallowUnknownFields()
		if err := d.Decode(&updated); err != nil || updated == nil || updated.ID != key || strings.TrimSpace(updated.Name) == "" || !validProfileInput(&profileCreateInput{Name: updated.Name, Observations: updated.Observations, Attributes: updated.Attributes, OnRegister: updated.OnRegister, Timeseries: updated.Timeseries, BootstrapTemplate: updated.BootstrapTemplate}) {
			WriteProblem(w, 400, "invalid_profile", "Invalid device profile")
			return
		}
		found, replaced := o.DeviceProfiles.ReplaceIfMatch(tenant, current, *updated)
		if !found {
			WriteProblem(w, 404, "not_found", "Unknown profile")
			return
		}
		if !replaced {
			current, found = o.DeviceProfiles.Get(tenant, key)
			if !found {
				WriteProblem(w, 404, "not_found", "Unknown profile")
				return
			}
			continue
		}
		body, err = json.Marshal(updated)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to encode profile")
			return
		}
		sum = sha256.Sum256(body)
		w.Header().Set("ETag", fmt.Sprintf("\"profile-v1-%x\"", sum))
		deviceJSON(w, 200, updated)
		return
	}
}
