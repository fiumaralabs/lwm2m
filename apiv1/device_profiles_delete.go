package apiv1

import (
	"net/http"
	"strings"
)

func (o Options) deleteDeviceProfile(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/v1/device-profiles/")
	if key == "" || strings.Contains(key, "/") {
		WriteProblem(w, 404, "not_found", "Unknown profile")
		return
	}
	// The memory store cannot detect references until devices can use profiles.
	found, inUse := o.DeviceProfiles.Delete(TenantFrom(r.Context()), key)
	if !found {
		WriteProblem(w, 404, "not_found", "Unknown profile")
		return
	}
	if inUse {
		WriteProblem(w, 409, "profile_in_use", "Profile is in use")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
