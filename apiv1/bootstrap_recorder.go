package apiv1

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// NewBootstrapSessionRecorder adapts a store for a single tenant. It excludes
// payloads, arbitrary error text and query parameters, which can contain keys.
// The caller must provide the correct tenant for the server's endpoints.
func NewBootstrapSessionRecorder(store BootstrapSessionStore, tenant string) func(bootstrap.Result) error {
	return func(r bootstrap.Result) error {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		s := BootstrapSession{ID: "bs_" + hex.EncodeToString(id[:]), StartedAt: r.StartedAt, EndedAt: r.EndedAt,
			Kind: "request", Status: "succeeded", Format: bootstrapFormat(r.Format), Steps: make([]BootstrapSessionStep, 0, len(r.Steps))}
		if r.Pack {
			s.Kind = "pack"
		}
		identity := struct {
			Mode        string `json:"mode"`
			PSKIdentity string `json:"pskIdentity,omitempty"`
		}{Mode: r.Identity.Mode.String()}
		if r.Identity.Mode == server.ModePSK {
			identity.PSKIdentity = r.Identity.PSKIdentity
		}
		s.Identity, _ = json.Marshal(identity)
		if r.Err != nil {
			s.Status = "failed"
			code := "bootstrap_failed"
			switch {
			case errors.Is(r.Err, bootstrap.ErrFinishRejected):
				code = "finish_rejected"
			case errors.Is(r.Err, bootstrap.ErrPackRefused):
				code = "pack_refused"
			case errors.Is(r.Err, bootstrap.ErrSessionTimeout):
				code = "session_timeout"
			case errors.Is(r.Err, bootstrap.ErrCancelled):
				code = "cancelled"
			}
			s.Error, _ = json.Marshal(map[string]string{"code": code})
		}
		for _, step := range r.Steps {
			path := step.Path.String()
			if step.Code == codes.POST {
				path = "/bs"
			}
			v := BootstrapSessionStep{Method: step.Code.String(), Path: path}
			if step.Result != 0 {
				v.Code = server.CodeString(step.Result)
			}
			if step.Format != nil {
				v.Format = bootstrapFormat(*step.Format)
			}
			s.Steps = append(s.Steps, v)
		}
		return store.Append(tenant, r.Endpoint, s)
	}
}

func bootstrapFormat(f lwm2m.ContentFormat) string {
	switch f.Canonical() {
	case lwm2m.FormatTLV:
		return "tlv"
	case lwm2m.FormatSenMLJSON:
		return "senml-json"
	case lwm2m.FormatSenMLCBOR:
		return "senml-cbor"
	case lwm2m.FormatLwM2MCBOR:
		return "lwm2m-cbor"
	case lwm2m.FormatLinkFormat:
		return "link-format"
	default:
		return f.String()
	}
}
