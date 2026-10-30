package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
		// Version the representation hash so future conditional writes can use
		// the same validator while comparing and updating atomically in the store.
		body, err := json.Marshal(p)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to encode profile")
			return
		}
		sum := sha256.Sum256(body)
		w.Header().Set("ETag", fmt.Sprintf("\"profile-v1-%x\"", sum))
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
	all := append([]DeviceProfile(nil), o.DeviceProfiles.List(tenant)...)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
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
