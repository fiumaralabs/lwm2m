package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// profileCursor binds the continuation to the authenticated tenant.
type profileCursor struct {
	Tenant string `json:"tenant"`
	After  string `json:"after"`
}

func (o Options) deviceProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Unsupported method")
		return
	}
	tenant := TenantFrom(r.Context())
	if r.URL.Path != "/v1/device-profiles" {
		key := strings.TrimPrefix(r.URL.Path, "/v1/device-profiles/")
		if key == "" || strings.Contains(key, "/") {
			WriteProblem(w, 404, "not_found", "Unknown profile")
			return
		}
		p, ok := o.DeviceProfiles.Get(tenant, key)
		if !ok {
			WriteProblem(w, 404, "not_found", "Unknown profile")
			return
		}
		deviceJSON(w, 200, p)
		return
	}
	q := r.URL.Query()
	for _, name := range []string{"limit", "cursor"} {
		if v, ok := q[name]; ok && (len(v) != 1 || v[0] == "") {
			WriteProblem(w, 400, "invalid_"+name, "Expected one nonempty value")
			return
		}
	}
	limit := 50
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			WriteProblem(w, 400, "invalid_limit", "Expected limit from 1 to 500")
			return
		}
		limit = n
	}
	after := ""
	if q.Get("cursor") != "" {
		b, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		var c profileCursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.After == "" {
			WriteProblem(w, 400, "invalid_cursor", "Invalid cursor")
			return
		}
		after = c.After
	}
	all := o.DeviceProfiles.List(tenant)
	items := make([]DeviceProfile, 0, limit)
	next := ""
	for _, p := range all {
		if p.ID <= after {
			continue
		}
		if len(items) == limit {
			b, _ := json.Marshal(profileCursor{tenant, items[len(items)-1].ID})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, p)
	}
	deviceJSON(w, 200, struct {
		Items []DeviceProfile `json:"items"`
		Next  string          `json:"nextCursor,omitempty"`
	}{items, next})
}
