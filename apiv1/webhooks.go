package apiv1

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/fiumaralabs/lwm2m/internal/id"
	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/store"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type webhookInput struct {
	URL      json.RawMessage `json:"url"`
	Types    json.RawMessage `json:"types"`
	Selector json.RawMessage `json:"selector"`
	Status   json.RawMessage `json:"status"`
}

func (o Options) webhooks(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	collection := r.URL.Path == "/v1/webhooks"
	key := strings.TrimPrefix(r.URL.Path, "/v1/webhooks/")
	rotate := strings.HasSuffix(key, "/rotate-secret")
	if rotate {
		key = strings.TrimSuffix(key, "/rotate-secret")
	}
	if !collection && (key == "" || strings.Contains(key, "/")) {
		WriteProblem(w, 404, "not_found", "Unknown API path")
		return
	}
	allow := "GET, PATCH, DELETE"
	if collection {
		allow = "GET, POST"
	}
	if rotate {
		allow = "POST"
	}
	if !strings.Contains(","+strings.ReplaceAll(allow, " ", "")+",", ","+r.Method+",") {
		w.Header().Set("Allow", allow)
		WriteProblem(w, 405, "method_not_allowed", "Unsupported method")
		return
	}
	fail := func(err error) bool {
		if err == nil {
			return false
		}
		code, status := "internal_error", 500
		if errors.Is(err, store.ErrWebhookNotFound) {
			code, status = "not_found", 404
		}
		if errors.Is(err, store.ErrWebhookCursor) {
			code, status = "invalid_cursor", 400
		}
		WriteProblem(w, status, code, "Webhook request failed")
		return true
	}
	if collection && r.Method == "GET" {
		for _, name := range []string{"limit", "cursor"} {
			if values, ok := r.URL.Query()[name]; ok && (len(values) != 1 || values[0] == "") {
				WriteProblem(w, 400, "invalid_"+name, "Expected one nonempty value")
				return
			}
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 500 {
				WriteProblem(w, 400, "invalid_limit", "Expected limit from 1 to 500")
				return
			}
			limit = n
		}
		items, next, err := o.Webhooks.List(tenant, r.URL.Query().Get("cursor"), limit)
		if fail(err) {
			return
		}
		deviceJSON(w, 200, struct {
			Items []store.Webhook `json:"items"`
			Next  string          `json:"nextCursor,omitempty"`
		}{items, next})
		return
	}
	if r.Method == "GET" {
		v, e := o.Webhooks.Get(tenant, key)
		if !fail(e) {
			deviceJSON(w, 200, v)
		}
		return
	}
	if r.Method == "DELETE" {
		if !fail(o.Webhooks.Delete(tenant, key)) {
			w.WriteHeader(204)
		}
		return
	}
	if r.Method == "PATCH" || collection {
		var input *webhookInput
		if !DecodeJSON(w, r, &input) {
			return
		}
		if input == nil {
			WriteProblem(w, 400, "invalid_json", "Expected object")
			return
		}
		p := store.WebhookPatch{}
		for _, field := range []struct {
			raw    json.RawMessage
			target any
		}{{input.URL, &p.URL}, {input.Types, &p.Types}, {input.Status, &p.Status}} {
			if field.raw != nil && (string(field.raw) == "null" || json.Unmarshal(field.raw, field.target) != nil) {
				WriteProblem(w, 400, "invalid_webhook", "Invalid field")
				return
			}
		}
		if input.Selector != nil {
			v := ""
			if string(input.Selector) != "null" && json.Unmarshal(input.Selector, &v) != nil {
				WriteProblem(w, 400, "invalid_selector", "Invalid selector")
				return
			}
			p.Selector = &v
		}
		if p.URL != nil {
			u, e := url.Parse(*p.URL)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.Contains(*p.URL, "#") {
				WriteProblem(w, 400, "invalid_url", "Expected absolute HTTP URL without userinfo or fragment")
				return
			}
		}
		if p.Types != nil {
			if len(*p.Types) == 0 {
				WriteProblem(w, 400, "invalid_types", "Event types required")
				return
			}
			for _, typ := range *p.Types {
				if !webhookType.MatchString(typ) {
					WriteProblem(w, 400, "invalid_types", "Invalid event pattern")
					return
				}
			}
		}
		if p.Status != nil && *p.Status != "active" && *p.Status != "disabled" {
			WriteProblem(w, 400, "invalid_status", "Expected active or disabled")
			return
		}
		if p.Selector != nil {
			if _, e := selector.Parse(*p.Selector); e != nil {
				WriteProblem(w, 400, "invalid_selector", "Invalid selector")
				return
			}
		}
		if collection && (p.URL == nil || p.Types == nil) {
			WriteProblem(w, 400, "invalid_webhook", "URL and types required")
			return
		}
		if !collection {
			v, e := o.Webhooks.Patch(tenant, key, p)
			if !fail(e) {
				deviceJSON(w, 200, v)
			}
			return
		}
		v := store.Webhook{ID: id.New("wh"), Status: "active"}
		if p.URL != nil {
			v.URL = *p.URL
		}
		if p.Types != nil {
			v.Types = *p.Types
		}
		if p.Selector != nil {
			v.Selector = *p.Selector
		}
		if p.Status != nil {
			v.Status = *p.Status
		}
		secret, ok := webhookSecret(w)
		if !ok {
			return
		}
		v.Secret = secret
		v, e := o.Webhooks.Create(tenant, v, r.Header.Get("Idempotency-Key"))
		if fail(e) {
			return
		}
		w.Header().Set("Location", "/v1/webhooks/"+v.ID)
		webhookSecretJSON(w, 201, v)
		return
	}
	secret, ok := webhookSecret(w)
	if !ok {
		return
	}
	v, e := o.Webhooks.Rotate(tenant, key, secret, r.Header.Get("Idempotency-Key"))
	if !fail(e) {
		webhookSecretJSON(w, 200, v)
	}
}
func webhookSecret(w http.ResponseWriter) (string, bool) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot generate secret")
		return "", false
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(random[:]), true
}
func webhookSecretJSON(w http.ResponseWriter, status int, v store.Webhook) {
	deviceJSON(w, status, struct {
		store.Webhook
		Secret string `json:"secret"`
	}{v, v.Secret})
}

var webhookType = regexp.MustCompile(`^lwm2m\.(?:[a-zA-Z0-9_-]+\.)*(?:[a-zA-Z0-9_-]+|\*)$`)
