package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m/model"
)

type builtinSummary struct {
	ID       uint16   `json:"id"`
	Name     string   `json:"name"`
	URN      string   `json:"urn"`
	Versions []string `json:"versions"`
	Class    string   `json:"class"`
	Source   string   `json:"source"`
}

var builtinModels = sync.OnceValues(func() (*model.Registry, []builtinSummary) {
	registry := model.Default()
	files, err := fs.Glob(model.CoreFS(), "registry/*.xml")
	if err != nil {
		panic(err)
	}
	seen := make(map[uint16]bool)
	for _, file := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(file, "registry/"), ".xml")
		id, err := strconv.ParseUint(strings.SplitN(name, "-", 2)[0], 10, 16)
		if err == nil {
			seen[uint16(id)] = true
		}
	}
	entries := make([]builtinSummary, 0, len(seen))
	// Numeric order keeps the list deterministic.
	for i := 0; i < 65536; i++ {
		id := uint16(i)
		if !seen[id] {
			continue
		}
		versions := registry.Versions(id)
		def, _ := registry.Get(id, versions[len(versions)-1])
		names := make([]string, 0, len(versions))
		for _, v := range versions {
			names = append(names, v.String())
		}
		urn := strings.TrimSuffix(def.URN, ":"+def.Version.String())
		entries = append(entries, builtinSummary{id, def.Name, urn, names, model.ObjectClass(id).String(), "builtin"})
	}
	return registry, entries
})

func serveModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registry, entries := builtinModels()
		if r.URL.Path == "/v1/models" {
			query := r.URL.Query()
			limit := 50
			var err error
			if query.Has("limit") {
				limit, err = strconv.Atoi(query.Get("limit"))
			}
			if err != nil || limit < 1 || limit > 500 {
				WriteProblem(w, 400, "invalid_limit", "limit must be between 1 and 500")
				return
			}
			q, class := strings.ToLower(query.Get("q")), query.Get("class")
			items := make([]builtinSummary, 0)
			for _, entry := range entries {
				if strings.Contains(strings.ToLower(entry.Name), q) && (class == "" || entry.Class == class) {
					items = append(items, entry)
				}
			}
			type cursor struct {
				Tenant, Q, Class string
				Offset           int
			}
			start := 0
			if raw := query.Get("cursor"); raw != "" {
				var c cursor
				b, e := base64.RawURLEncoding.DecodeString(raw)
				if e != nil || json.Unmarshal(b, &c) != nil || c.Tenant != TenantFrom(r.Context()) || c.Q != q || c.Class != class || c.Offset < 1 || c.Offset >= len(items) {
					WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
					return
				}
				start = c.Offset
			}
			end, next := start+limit, ""
			if end < len(items) {
				b, _ := json.Marshal(cursor{TenantFrom(r.Context()), q, class, end})
				next = base64.RawURLEncoding.EncodeToString(b)
			} else {
				end = len(items)
			}
			deviceJSON(w, 200, struct {
				Items []builtinSummary `json:"items"`
				Next  string           `json:"nextCursor,omitempty"`
			}{items[start:end], next})
			return
		}
		raw := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		id, err := strconv.ParseUint(raw, 10, 16)
		if err != nil {
			WriteProblem(w, 404, "not_found", "Unknown model")
			return
		}
		versions := registry.Versions(uint16(id))
		if len(versions) == 0 {
			WriteProblem(w, 404, "not_found", "Unknown model")
			return
		}
		version := versions[len(versions)-1]
		if query := r.URL.Query().Get("version"); query != "" {
			v, err := model.ParseVersion(query)
			if err != nil {
				WriteProblem(w, 404, "not_found", "Unknown model version")
				return
			}
			found := false
			for _, known := range versions {
				if known == v {
					found = true
					version = known
					break
				}
			}
			if !found {
				WriteProblem(w, 404, "not_found", "Unknown model version")
				return
			}
		}
		def, _ := registry.Get(uint16(id), version)
		deviceJSON(w, 200, definitionView(def))
	})).ServeHTTP(w, r)
}
