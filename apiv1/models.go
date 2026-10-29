package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"sort"
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

func serveModels(store ModelStore, w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/models" || r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/versions/") {
		RequireScope("devices:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mutateModel(store, w, r) })).ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet {
		allow := "GET"
		if r.URL.Path == "/v1/models" {
			allow = "GET, POST"
		}
		w.Header().Set("Allow", allow)
		WriteProblem(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registry, base := builtinModels()
		entries := append([]builtinSummary(nil), base...)
		uploads := store.List(TenantFrom(r.Context()))
		sort.Slice(uploads, func(i, j int) bool { return uploads[j].Version.Less(uploads[i].Version) })
		for _, object := range uploads {
			found := false
			for i := range entries {
				if entries[i].ID == object.ID {
					latest, _ := model.ParseVersion(entries[i].Versions[len(entries[i].Versions)-1])
					if latest.Less(object.Version) {
						entries[i].Name = object.Name
						entries[i].URN = strings.TrimSuffix(object.URN, ":"+object.Version.String())
						entries[i].Source = "tenant"
					}
					entries[i].Versions = append(append([]string(nil), entries[i].Versions...), object.Version.String())
					found = true
					break
				}
			}
			if !found {
				entries = append(entries, builtinSummary{object.ID, object.Name, strings.TrimSuffix(object.URN, ":"+object.Version.String()), []string{object.Version.String()}, model.ObjectClass(object.ID).String(), "tenant"})
			}
		}
		for i := range entries {
			sort.Slice(entries[i].Versions, func(a, b int) bool {
				x, _ := model.ParseVersion(entries[i].Versions[a])
				y, _ := model.ParseVersion(entries[i].Versions[b])
				return x.Less(y)
			})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
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
		for _, object := range uploads {
			if object.ID == uint16(id) {
				versions = append(versions, object.Version)
			}
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Less(versions[j]) })
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
		for _, object := range uploads {
			if object.ID == uint16(id) && object.Version == version {
				deviceJSON(w, 200, definitionView(object))
				return
			}
		}
		def, _ := registry.Get(uint16(id), version)
		deviceJSON(w, 200, definitionView(def))
	})).ServeHTTP(w, r)
}

func mutateModel(store ModelStore, w http.ResponseWriter, r *http.Request) {
	registry, _ := builtinModels()
	tenant := TenantFrom(r.Context())
	if r.Method == http.MethodPost {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/xml" {
			WriteProblem(w, 415, "unsupported_media_type", "Expected application/xml")
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil || len(data) > 1<<20 {
			WriteProblem(w, 400, "invalid_model", "Cannot read model or model exceeds 1 MiB")
			return
		}
		if err := validateModelXML(data); err != nil {
			WriteProblem(w, 400, "invalid_model", err.Error())
			return
		}
		objects, err := model.ParseXML(data)
		if err != nil {
			WriteProblem(w, 400, "invalid_model", err.Error())
			return
		}
		if len(objects) != 1 {
			WriteProblem(w, 400, "invalid_model", "Expected one object")
			return
		}
		object := objects[0]
		for _, v := range registry.Versions(object.ID) {
			if v == object.Version {
				WriteProblem(w, 409, "conflict", "Built-in model cannot be replaced")
				return
			}
		}
		store.Put(tenant, object)
		deviceJSON(w, 201, struct {
			*modelDefinition
			Source string `json:"source"`
		}{definitionView(object), "tenant"})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/models/"), "/")
	if len(parts) != 3 || parts[1] != "versions" {
		WriteProblem(w, 404, "not_found", "Unknown model")
		return
	}
	id, err := strconv.ParseUint(parts[0], 10, 16)
	version, e := model.ParseVersion(parts[2])
	if err != nil || e != nil {
		WriteProblem(w, 404, "not_found", "Unknown model")
		return
	}
	for _, v := range registry.Versions(uint16(id)) {
		if v == version {
			WriteProblem(w, 409, "conflict", "Built-in model cannot be removed")
			return
		}
	}
	if !store.Delete(tenant, uint16(id), version) {
		WriteProblem(w, 404, "not_found", "Unknown model")
		return
	}
	w.WriteHeader(204)
}
