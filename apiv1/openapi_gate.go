package apiv1

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type openAPIRoute struct {
	segments     []string
	methods      map[string]bool
	literalCount int
}

// Build the gate from the same immutable document served to clients.
var openAPIGateRoutes = func() []openAPIRoute {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		panic(err)
	}
	if len(doc.Paths) == 0 {
		panic("OpenAPI document has no paths")
	}
	var routes []openAPIRoute
	for path, item := range doc.Paths {
		route := openAPIRoute{segments: strings.Split(path, "/"), methods: make(map[string]bool)}
		for _, segment := range route.segments {
			if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
				route.literalCount++
			}
		}
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			if _, ok := item[method]; ok {
				route.methods[strings.ToUpper(method)] = true
			}
		}
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].literalCount > routes[j].literalCount })
	return routes
}()

func openAPIGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.EscapedPath(), "/")
		for _, route := range openAPIGateRoutes {
			if len(parts) != len(route.segments) {
				continue
			}
			match := true
			for i, template := range route.segments {
				part, err := url.PathUnescape(parts[i])
				if err != nil || (strings.HasPrefix(template, "{") && strings.HasSuffix(template, "}") && part == "") || (!(strings.HasPrefix(template, "{") && strings.HasSuffix(template, "}")) && template != part) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if route.methods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			var allow []string
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"} {
				if route.methods[method] {
					allow = append(allow, method)
				}
			}
			w.Header().Set("Allow", strings.Join(allow, ", "))
			WriteProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		WriteProblem(w, http.StatusNotFound, "not_found", "Unknown API path")
	})
}
