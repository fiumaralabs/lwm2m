package apiv1

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/id"
)

// NewTrustAnchorHandler returns an authenticated-context handler for future router wiring.
// Callers must wrap it in Authenticate; scope enforcement is performed here.
func NewTrustAnchorHandler(store TrustAnchorStore) http.Handler {
	return RequireScope("credentials:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const path = "/v1/trust-anchors"
		collection := r.URL.Path == path
		key := strings.TrimPrefix(r.URL.Path, path+"/")
		if !collection && (!strings.HasPrefix(r.URL.Path, path+"/") || key == "" || strings.Contains(key, "/")) {
			WriteProblem(w, 404, "not_found", "Unknown trust anchor")
			return
		}
		tenant := TenantFrom(r.Context())
		switch {
		case collection && r.Method == http.MethodPost:
			var in *struct {
				Name         string `json:"name"`
				Certificates string `json:"certificates"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if !DecodeJSON(w, r, &in) {
				return
			}
			if in == nil || strings.TrimSpace(in.Name) == "" {
				WriteProblem(w, 400, "invalid_trust_anchor", "Name and CA certificates are required")
				return
			}
			certs, ok := parseTrustAnchorCertificates(in.Certificates)
			if !ok {
				WriteProblem(w, 400, "invalid_trust_anchor", "Expected valid, unexpired CA certificates")
				return
			}
			a := TrustAnchor{ID: id.New("ta"), Name: in.Name, PEM: in.Certificates, Certificates: certs}
			if err := store.Create(tenant, a); err != nil {
				switch {
				case errors.Is(err, ErrTrustAnchorDuplicate):
					WriteProblem(w, 409, "duplicate_trust_anchor", "Certificate already exists")
				case errors.Is(err, ErrTrustAnchorLimit):
					WriteProblem(w, 503, "capacity_exceeded", "Trust anchor capacity reached")
				default:
					WriteProblem(w, 500, "internal_error", "Unable to create trust anchor")
				}
				return
			}
			w.Header().Set("Location", path+"/"+a.ID)
			deviceJSON(w, 201, a)
		case collection && r.Method == http.MethodGet:
			listTrustAnchors(w, r, store, tenant)
		case !collection && r.Method == http.MethodGet:
			a, ok := store.Get(tenant, key)
			if !ok {
				WriteProblem(w, 404, "not_found", "Unknown trust anchor")
				return
			}
			deviceJSON(w, 200, a)
		case !collection && r.Method == http.MethodDelete:
			if !store.Delete(tenant, key) {
				WriteProblem(w, 404, "not_found", "Unknown trust anchor")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			if collection {
				w.Header().Set("Allow", "GET, POST")
			} else {
				w.Header().Set("Allow", "GET, DELETE")
			}
			WriteProblem(w, 405, "method_not_allowed", "Unsupported method")
		}
	}))
}
func parseTrustAnchorCertificates(bundle string) ([]TrustAnchorCertificate, bool) {
	data := bytes.TrimSpace([]byte(bundle))
	certs := make([]TrustAnchorCertificate, 0)
	seen := map[string]bool{}
	for len(data) > 0 {
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, false
		}
		endMarker := []byte("-----END CERTIFICATE-----")
		end := bytes.Index(data, endMarker)
		if end < 0 || bytes.Count(data[:end], []byte("-----BEGIN ")) != 1 {
			return nil, false
		}
		end += len(endMarker)
		block, remainder := pem.Decode(data[:end])
		rest := data[end:]
		if block == nil || len(bytes.TrimSpace(remainder)) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) > 0 {
			return nil, false
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.BasicConstraintsValid || !cert.IsCA || !time.Now().Before(cert.NotAfter) {
			return nil, false
		}
		sum := sha256.Sum256(cert.Raw)
		fingerprint := hex.EncodeToString(sum[:])
		if seen[fingerprint] {
			return nil, false
		}
		seen[fingerprint] = true
		certs = append(certs, TrustAnchorCertificate{Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), NotBefore: cert.NotBefore, NotAfter: cert.NotAfter, Fingerprint: fingerprint})
		data = bytes.TrimSpace(rest)
	}
	return certs, len(certs) > 0
}

type trustAnchorCursor struct {
	Tenant string `json:"tenant"`
	After  string `json:"after"`
}

func listTrustAnchors(w http.ResponseWriter, r *http.Request, store TrustAnchorStore, tenant string) {
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
		var c trustAnchorCursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.After == "" {
			WriteProblem(w, 400, "invalid_cursor", "Invalid cursor")
			return
		}
		after = c.After
	}
	all := store.List(tenant)
	items := make([]TrustAnchor, 0, limit)
	next := ""
	for _, a := range all {
		if a.ID <= after {
			continue
		}
		if len(items) == limit {
			b, _ := json.Marshal(trustAnchorCursor{tenant, items[len(items)-1].ID})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, a)
	}
	deviceJSON(w, 200, struct {
		Items []TrustAnchor `json:"items"`
		Next  string        `json:"nextCursor,omitempty"`
	}{items, next})
}
