package apiv1

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTrustAnchorPagination(t *testing.T) {
	s := NewMemoryTrustAnchorStore(4)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Create("one", TrustAnchor{ID: id, Certificates: []TrustAnchorCertificate{{Fingerprint: id}}}); err != nil {
			t.Fatal(err)
		}
	}
	h := NewTrustAnchorHandler(s)
	get := func(tenant, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: ",credentials:write,"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/trust-anchors?limit=1"
	var ids []string
	for i := 0; i < 3; i++ {
		w := get("one", path)
		var page struct {
			Items []TrustAnchor `json:"items"`
			Next  string        `json:"nextCursor"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
			t.Fatalf("page: %d %s", w.Code, w.Body.String())
		}
		ids = append(ids, page.Items[0].ID)
		if i < 2 {
			if page.Next == "" {
				t.Fatal("missing cursor")
			}
			if other := get("two", "/v1/trust-anchors?cursor="+page.Next); other.Code != 400 {
				t.Fatalf("foreign cursor %d", other.Code)
			}
			path = "/v1/trust-anchors?limit=1&cursor=" + page.Next
		} else if page.Next != "" {
			t.Fatal("extra cursor")
		}
	}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("ids %v", ids)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=1&limit=2", "cursor=invalid"} {
		if w := get("one", "/v1/trust-anchors?"+q); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
}
