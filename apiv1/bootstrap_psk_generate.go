package apiv1

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fiumaralabs/lwm2m/server"
)

// generateBootstrapPSKs returns a non-nil response marker when it has written a generated config.
func (o Options) generateBootstrapPSKs(w http.ResponseWriter, tenant, ep string, body []byte, input bootstrapInput) (any, bool) {
	count, selfCount := 0, 0
	for _, entry := range input.Servers {
		if entry.Security.PSK != nil && entry.Security.PSK.Generate {
			count++
			if entry.Server == "self" {
				selfCount++
			}
		}
	}
	if count == 0 {
		return nil, true
	}
	if selfCount > 1 {
		WriteProblem(w, 422, "validation_error", "Only one generated self credential is supported")
		return nil, false
	}
	if selfCount > 0 && (o.Server == nil || tenant != o.ServerTenant) {
		WriteProblem(w, 404, "not_found", "Device not found")
		return nil, false
	}
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&config) != nil {
		WriteProblem(w, 400, "invalid_json", "Invalid bootstrap config")
		return nil, false
	}
	servers := config["servers"].([]any)
	type generatedPSK struct {
		index             int
		identity, encoded string
		key               []byte
		self              bool
	}
	generated := make([]generatedPSK, 0, count)
	for i, entry := range input.Servers {
		if entry.Security.PSK == nil || !entry.Security.PSK.Generate {
			continue
		}
		var random [48]byte
		if _, err := rand.Read(random[:]); err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot generate credentials")
			return nil, false
		}
		p := generatedPSK{index: i, identity: hex.EncodeToString(random[:16]), key: random[16:], encoded: "base64:" + base64.StdEncoding.EncodeToString(random[16:]), self: entry.Server == "self"}
		generated = append(generated, p)
		security := servers[i].(map[string]any)["security"].(map[string]any)
		security["psk"] = map[string]any{"identity": p.identity, "key": p.encoded}
	}
	stored, err := json.Marshal(config)
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot encode bootstrap config")
		return nil, false
	}
	for _, p := range generated {
		if !p.self {
			continue
		}
		_, existed := o.Server.Security().ByEndpoint(ep)
		err := o.Server.Security().Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: p.identity, PSKKey: p.key})
		if errors.Is(err, server.ErrDuplicatePSKIdentity) {
			WriteProblem(w, 409, "duplicate_psk_identity", "PSK identity already exists")
			return nil, false
		}
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot store credential")
			return nil, false
		}
		if existed {
			o.Server.RemoveRegistration(ep)
		}
	}
	o.BootstrapConfigs.Put(tenant, ep, stored)
	redactBootstrap(config)
	for _, p := range generated {
		security := servers[p.index].(map[string]any)["security"].(map[string]any)
		security["psk"].(map[string]any)["key"] = p.encoded
	}
	w.Header().Set("Cache-Control", "no-store")
	deviceJSON(w, 200, config)
	return config, true
}
