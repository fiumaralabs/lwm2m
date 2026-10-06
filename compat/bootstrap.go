package compat

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/server"
)

// BootstrapStore is the config store the bootstrap REST edits;
// *bootstrap.MemoryConfigStore implements it.
type BootstrapStore interface {
	bootstrap.ConfigStore
	Put(ep string, c *bootstrap.BootstrapConfig) error
	Delete(ep string) bool
}

// NewBootstrap returns the Leshan bootstrap server demo REST
// (zephyr-interop.md §4.2), Leshan's :8081: <prefix>/security/clients over
// the bootstrap credentials and POST/DELETE <prefix>/bootstrap/<ep> over
// the configs. Pass the stores of the running bootstrap.Server.
func NewBootstrap(prefix string, configs BootstrapStore, security server.SecurityStore) http.Handler {
	if prefix == "" {
		prefix = "/api"
	}
	prefix = strings.TrimSuffix(prefix, "/")
	mux := http.NewServeMux()
	mux.HandleFunc(prefix+"/security/", func(w http.ResponseWriter, r *http.Request) {
		serveSecurity(w, r, security, prefix)
	})
	mux.HandleFunc(prefix+"/bootstrap/", func(w http.ResponseWriter, r *http.Request) {
		ep := strings.Join(segments(strings.TrimPrefix(r.URL.Path, prefix+"/bootstrap")), "/")
		switch {
		case ep == "":
			// ponytail: GET /bootstrap (list all, BS:71-79) is unused by the harness.
			textError(w, http.StatusBadRequest, "path format must be /bootstrap/<endpoint>")
		case r.Method == http.MethodPost:
			var c bootstrap.BootstrapConfig
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				textError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
				return
			}
			if err := configs.Put(ep, &c); err != nil {
				textError(w, http.StatusBadRequest, err.Error()) // InvalidConfigurationException
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			if !configs.Delete(ep) {
				textError(w, http.StatusNotFound, "no config for "+ep)
				return
			}
			w.WriteHeader(http.StatusNoContent) // BS:155-163
		default:
			textError(w, http.StatusMethodNotAllowed, "")
		}
	})
	return mux
}
