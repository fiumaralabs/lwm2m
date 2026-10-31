package apiv1

import (
	"crypto/tls"
	"errors"
	"net/http"

	"github.com/fiumaralabs/lwm2m/internal/id"
	"github.com/fiumaralabs/lwm2m/selector"
)

type estInput struct {
	Enabled bool `json:"enabled"`
	Issuer  struct {
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
	} `json:"issuer"`
	ValidityDays int    `json:"validityDays"`
	Selector     string `json:"selector"`
}
type estResponse struct {
	Enabled bool `json:"enabled"`
	Issuer  struct {
		Certificate string `json:"certificate"`
	} `json:"issuer"`
	ValidityDays int    `json:"validityDays"`
	Selector     string `json:"selector"`
}

func estPublic(c ESTConfig) estResponse {
	var out estResponse
	out.Enabled = c.Enabled
	out.Issuer.Certificate = c.Certificate
	out.ValidityDays = c.ValidityDays
	out.Selector = c.Selector
	return out
}

// NewESTConfigHandler requires an authenticated context and enforces credentials scope.
// It deliberately does not wire the endpoint into the router.
func NewESTConfigHandler(configs ESTConfigStore, anchors TrustAnchorStore) http.Handler {
	return RequireScope("credentials:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/est" {
			WriteProblem(w, 404, "not_found", "Unknown EST path")
			return
		}
		tenant := TenantFrom(r.Context())
		switch r.Method {
		case http.MethodGet:
			c, ok := configs.Get(tenant)
			if !ok {
				WriteProblem(w, 404, "not_found", "EST configuration not found")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			deviceJSON(w, 200, estPublic(c))
		case http.MethodPut:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			var in *estInput
			if !DecodeJSON(w, r, &in) {
				return
			}
			if in == nil {
				WriteProblem(w, 400, "invalid_est_config", "Invalid EST configuration")
				return
			}
			certs, valid := parseTrustAnchorCertificates(in.Issuer.Certificate)
			_, keyErr := tls.X509KeyPair([]byte(in.Issuer.Certificate), []byte(in.Issuer.PrivateKey))
			_, selectorErr := selector.Parse(in.Selector)
			if !valid || len(certs) != 1 || keyErr != nil || in.ValidityDays < 1 || in.ValidityDays > 3650 || selectorErr != nil {
				WriteProblem(w, 400, "invalid_est_config", "Invalid issuer, validity or selector")
				return
			}
			anchor := TrustAnchor{ID: id.New("ta"), Name: "EST issuer", PEM: in.Issuer.Certificate, Certificates: certs}
			if err := anchors.Create(tenant, anchor); err != nil && !errors.Is(err, ErrTrustAnchorDuplicate) {
				WriteProblem(w, 500, "internal_error", "Cannot register EST issuer")
				return
			}
			c := ESTConfig{Enabled: in.Enabled, Certificate: in.Issuer.Certificate, PrivateKey: in.Issuer.PrivateKey, ValidityDays: in.ValidityDays, Selector: in.Selector}
			status := 201
			if configs.Put(tenant, c) {
				status = 200
			}
			w.Header().Set("Cache-Control", "no-store")
			deviceJSON(w, status, estPublic(c))
		default:
			w.Header().Set("Allow", "GET, PUT")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET or PUT")
		}
	}))
}
