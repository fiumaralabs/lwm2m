package apiv1

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type firmwareUpdateRequest struct {
	Firmware           string `json:"firmware"`
	URI                string `json:"uri"`
	Method             string `json:"method"`
	Severity           string `json:"severity"`
	MaxDeferSeconds    *int   `json:"maxDeferSeconds"`
	AutoUpgrade        bool   `json:"autoUpgrade"`
	Resume             bool   `json:"resume"`
	PushTimeoutSeconds int    `json:"pushTimeoutSeconds"`
	PushAttempts       int    `json:"pushAttempts"`
	PollSeconds        int    `json:"pollSeconds"`
}
type firmwareUpdateCursor struct {
	Tenant    string    `json:"tenant"`
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

// NewFirmwareUpdatesHandler serves firmware update resources without router wiring.
func NewFirmwareUpdatesHandler(store FirmwareUpdateStore, firmware FirmwareStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		device, id := "", ""
		if strings.HasPrefix(path, "/v1/devices/") && strings.HasSuffix(path, "/firmware-updates") {
			device = strings.TrimSuffix(strings.TrimPrefix(path, "/v1/devices/"), "/firmware-updates")
			if device == "" || strings.Contains(device, "/") {
				WriteProblem(w, 404, "not_found", "Unknown resource")
				return
			}
		} else if strings.HasPrefix(path, "/v1/firmware-updates/") {
			id = strings.TrimPrefix(path, "/v1/firmware-updates/")
			if id == "" || strings.Contains(id, "/") {
				WriteProblem(w, 404, "not_found", "Unknown resource")
				return
			}
		} else {
			WriteProblem(w, 404, "not_found", "Unknown resource")
			return
		}
		if !(device != "" && (r.Method == http.MethodGet || r.Method == http.MethodPost) || id != "" && (r.Method == http.MethodGet || r.Method == http.MethodDelete)) {
			if device != "" {
				w.Header().Set("Allow", "GET, POST")
			} else {
				w.Header().Set("Allow", "GET, DELETE")
			}
			WriteProblem(w, 405, "method_not_allowed", "Method not allowed")
			return
		}
		scope := "firmware:write"
		if r.Method == http.MethodGet {
			scope = "devices:read"
		}
		RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := TenantFrom(r.Context())
			if device != "" && r.Method == http.MethodPost {
				var req firmwareUpdateRequest
				if !DecodeJSON(w, r, &req) {
					return
				}
				u := FirmwareUpdate{Device: device, Firmware: req.Firmware, URI: req.URI, Method: req.Method, Severity: req.Severity, MaxDeferSeconds: req.MaxDeferSeconds, AutoUpgrade: req.AutoUpgrade, Resume: req.Resume, PushTimeoutSeconds: req.PushTimeoutSeconds, PushAttempts: req.PushAttempts, PollSeconds: req.PollSeconds, Status: "pending", CreatedAt: time.Now().UTC()}
				if err := ValidateFirmwareUpdate(&u); err != nil {
					WriteProblem(w, 400, "invalid_request", err.Error())
					return
				}
				if u.Firmware != "" {
					if _, ok := firmware.Get(tenant, u.Firmware); !ok {
						WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
						return
					}
				}
				b := make([]byte, 16)
				if _, err := rand.Read(b); err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot generate update ID")
					return
				}
				u.ID = "fwu_" + hex.EncodeToString(b)
				if err := store.Create(tenant, u); err != nil {
					if errors.Is(err, ErrUpdateInProgress) {
						WriteProblem(w, 409, "update_in_progress", "Device already has an active update")
					} else {
						WriteProblem(w, 500, "internal_error", "Cannot store update")
					}
					return
				}
				deviceJSON(w, 201, u)
				return
			}
			if device != "" {
				listFirmwareUpdates(store, w, r, tenant, device)
				return
			}
			u, ok := store.Get(tenant, id)
			if !ok {
				WriteProblem(w, 404, "not_found", "Unknown firmware update")
				return
			}
			if r.Method == http.MethodGet {
				deviceJSON(w, 200, u)
				return
			}
			if !store.UpdateStatus(tenant, id, "cancelled") {
				WriteProblem(w, 409, "invalid_state", "Firmware update is already terminal")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r)
	})
}

func listFirmwareUpdates(store FirmwareUpdateStore, w http.ResponseWriter, r *http.Request, tenant, device string) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["limit"]) > 1 || len(q["cursor"]) > 1 {
		WriteProblem(w, 400, "invalid_query", "Invalid query")
		return
	}
	limit := 50
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 500 {
		WriteProblem(w, 400, "invalid_limit", "Expected limit from 1 to 500")
		return
	}
	var cursor firmwareUpdateCursor
	if q.Has("cursor") {
		b, e := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if e != nil || json.Unmarshal(b, &cursor) != nil || cursor.Tenant != tenant || cursor.Device != device || cursor.ID == "" || cursor.CreatedAt.IsZero() {
			WriteProblem(w, 400, "invalid_cursor", "Invalid cursor")
			return
		}
	}
	items := make([]FirmwareUpdate, 0, limit)
	next := ""
	for _, u := range store.ListForDevice(tenant, device) {
		if cursor.ID != "" && (u.CreatedAt.After(cursor.CreatedAt) || u.CreatedAt.Equal(cursor.CreatedAt) && u.ID >= cursor.ID) {
			continue
		}
		if len(items) == limit {
			last := items[len(items)-1]
			b, _ := json.Marshal(firmwareUpdateCursor{tenant, device, last.CreatedAt, last.ID})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, u)
	}
	deviceJSON(w, 200, struct {
		Items []FirmwareUpdate `json:"items"`
		Next  string           `json:"nextCursor,omitempty"`
	}{items, next})
}
